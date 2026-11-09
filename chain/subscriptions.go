package chain

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/fancan/fans-kit/chain/provider"
	"github.com/fancan/fans-kit/chain/subscription"
)

type logRecoveryError struct{ err error }

func (e *logRecoveryError) Error() string { return "log recovery failed: " + e.err.Error() }
func (e *logRecoveryError) Unwrap() error { return e.err }

// Subscribe 建立逻辑WS订阅 注意：会自动重连与重新订阅。
// 通用订阅无法保证补发断线期间遗漏的消息。
func (i *Instance) Subscribe(ctx context.Context, namespace string, channel any, args ...any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(i.wsPool.All()) == 0 {
		return fmt.Errorf("chain %s: no websocket providers configured", i.name)
	}
	v := reflect.ValueOf(channel)
	if v.Kind() != reflect.Chan || v.Type().ChanDir() == reflect.RecvDir || v.IsNil() {
		return fmt.Errorf("subscription channel must be writable")
	}
	return i.startSubscription(ctx, func(runCtx context.Context) { i.subscribeLoop(runCtx, namespace, channel, args...) })
}

// 订阅循环
func (i *Instance) subscribeLoop(ctx context.Context, namespace string, channel any, args ...any) {
	for ctx.Err() == nil {
		connected := false
		for _, ep := range i.wsPool.Select(0) {
			if ctx.Err() != nil {
				return
			}
			if !ep.Acquire(time.Now()) {
				continue
			}
			if err := i.runSubscription(ctx, ep, namespace, channel, args...); err == nil {
				connected = true
			} else {
				if ctx.Err() != nil {
					ep.ReleaseProbe()
					return
				}
				if errors.Is(err, rpc.ErrNotificationsUnsupported) || (isRPCResponseError(err) && !retryableError(err)) {
					i.recordWS(ep, nil, 0)
				} else {
					i.recordWS(ep, err, 0)
				}
			}
			if ctx.Err() != nil {
				return
			}
			if connected {
				break
			}
		}
		timer := time.NewTimer(i.cfg.WebSocket.ReconnectInterval.Value())
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (i *Instance) runSubscription(ctx context.Context, ep *provider.Endpoint, namespace string, channel any, args ...any) error {
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sub, err := ep.Subscribe(subCtx, namespace, channel, args...)
	if err != nil {
		return err
	}
	i.recordWS(ep, nil, 0)
	defer sub.Unsubscribe()
	select {
	case <-ctx.Done():
		return nil
	case err, ok := <-sub.Err():
		if !ok {
			return fmt.Errorf("subscription closed")
		}
		if err == nil {
			return fmt.Errorf("subscription ended")
		}
		return err
	}
}

// HeadStream 订阅新区块头，返回区块号通道。内部复用Subscribe的自动重连逻辑；
// 调用方取消或实例关闭时通道被关闭。无WS提供商时返回错误。
func (i *Instance) HeadStream(ctx context.Context) (<-chan uint64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	headerCh := make(chan *types.Header, i.cfg.WebSocket.SendBuffer)
	if err := i.Subscribe(ctx, "eth", headerCh, "newHeads"); err != nil {
		return nil, err
	}
	runCtx, cleanup := i.bindContext(ctx)
	out := make(chan uint64, 64)
	go func() {
		defer close(out)
		defer cleanup()
		for {
			select {
			case <-runCtx.Done():
				return
			case h, ok := <-headerCh:
				if !ok {
					return
				}
				if h == nil {
					continue
				}
				select {
				case out <- h.Number.Uint64():
				case <-runCtx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// PendingTxStream 订阅公共内存池的 newPendingTransactions（pending tx hash 流）。
// 语义与HeadStream一致：自动重连，调用方取消或实例关闭时通道被关闭。
func (i *Instance) PendingTxStream(ctx context.Context) (<-chan common.Hash, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	hashCh := make(chan common.Hash, 1024)
	if err := i.Subscribe(ctx, "eth", hashCh, "newPendingTransactions"); err != nil {
		return nil, err
	}
	runCtx, cleanup := i.bindContext(ctx)
	out := make(chan common.Hash, 1024)
	go func() {
		defer close(out)
		defer cleanup()
		for {
			select {
			case <-runCtx.Done():
				return
			case h, ok := <-hashCh:
				if !ok {
					return
				}
				select {
				case out <- h:
				case <-runCtx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// SubscribeLogs 维护eth_subscribe(logs)流，并通过eth_getLogs补全缺口。
// 通过q.FromBlock指定历史回放起点；为 nil 时从当前链头开始（启用日志恢复时，该链头在WS订阅建立后再回填）。ToBlock/BlockHash不适用于实时订阅。
func (i *Instance) SubscribeLogs(ctx context.Context, q ethereum.FilterQuery) (<-chan types.Log, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if q.BlockHash != nil {
		return nil, fmt.Errorf("SubscribeLogs does not support blockHash filters; use GetLogs for a single block hash")
	}
	if q.ToBlock != nil {
		return nil, fmt.Errorf("SubscribeLogs is a live subscription; ToBlock is not supported")
	}
	out := make(chan types.Log, i.cfg.WebSocket.SendBuffer)
	if len(i.wsPool.All()) == 0 {
		return nil, fmt.Errorf("chain %s: no websocket providers", i.name)
	}
	var cursor uint64
	var floor uint64
	if q.FromBlock != nil {
		if q.FromBlock.Sign() < 0 {
			return nil, fmt.Errorf("negative from block")
		}
		if !q.FromBlock.IsUint64() {
			return nil, fmt.Errorf("from block exceeds uint64")
		}
		cursor = q.FromBlock.Uint64()
		floor = cursor
		if cursor > 0 {
			cursor--
		}
	} else {
		n, err := i.BlockNumber(ctx)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			cursor = n - 1
		} else {
			cursor = 0
		}
		floor = n
	}
	dedup := subscription.NewDeduper(i.cfg.Recovery.DedupCapacity)
	delivered := make(map[string]types.Log)
	var cursorHash common.Hash
	err := i.startSubscription(ctx, func(runCtx context.Context) {
		defer close(out)
		for runCtx.Err() == nil {
			connected := false
			for _, ep := range i.wsPool.Select(0) {
				if runCtx.Err() != nil {
					return
				}
				if !ep.Acquire(time.Now()) {
					continue
				}
				if err := i.runLogSubscription(runCtx, ep, q, &cursor, floor, &cursorHash, dedup, delivered, out); err == nil {
					connected = true
				} else {
					if runCtx.Err() != nil {
						ep.ReleaseProbe()
						return
					}
					var recoveryErr *logRecoveryError
					if errors.As(err, &recoveryErr) || errors.Is(err, rpc.ErrNotificationsUnsupported) || (isRPCResponseError(err) && !retryableError(err)) {
						i.recordWS(ep, nil, 0)
					} else {
						i.recordWS(ep, err, 0)
					}
				}
				if runCtx.Err() != nil {
					return
				}
				if connected {
					break
				}
			}
			t := time.NewTimer(i.cfg.WebSocket.ReconnectInterval.Value())
			select {
			case <-runCtx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// runLogSubscription 在单个端点上执行一次日志订阅生命周期：建立WS订阅后回填历史，
// 订阅存续期间转发日志、更新游标并做去重；检测到重组时回退并重放。
func (i *Instance) runLogSubscription(ctx context.Context, ep *provider.Endpoint, q ethereum.FilterQuery, cursor *uint64, floor uint64, cursorHash *common.Hash, dedup *subscription.Deduper, delivered map[string]types.Log, out chan<- types.Log) error {
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := make(chan types.Log, i.cfg.WebSocket.SendBuffer)
	subArg := logSubscriptionArg(q)
	// 订阅 eth_subscribe("logs", filter)：namespace 传 "eth"，子类型 "logs" 作为首个参数。
	sub, err := ep.Subscribe(subCtx, "eth", ch, "logs", subArg)
	if err != nil {
		return err
	}
	i.recordWS(ep, nil, 0)
	defer sub.Unsubscribe()
	// Subscribe first, then backfill, closing the race between the HTTP snapshot and WS setup.
	if i.cfg.Recovery.Enabled {
		// Detect a changed canonical hash at the last checkpoint. Rewind a bounded
		// window so replacement logs can be fetched; dedup keys include block hash.
		if *cursor > 0 && *cursorHash != (common.Hash{}) {
			h, hErr := i.HeaderByNumber(subCtx, new(big.Int).SetUint64(*cursor))
			if hErr != nil {
				return &logRecoveryError{err: hErr}
			}
			if h != nil && h.Hash() != *cursorHash {
				oldCursor := *cursor
				depth := i.cfg.Recovery.ReorgDepth
				rewind := uint64(0)
				if depth < oldCursor {
					rewind = oldCursor - depth
				}
				if rewind < floor {
					rewind = floor
				}
				if err = i.emitOrphanedLogs(subCtx, q, rewind, oldCursor, dedup, delivered, out); err != nil {
					return &logRecoveryError{err: err}
				}
				if rewind > 0 {
					*cursor = rewind - 1
				} else {
					*cursor = 0
				}
				*cursorHash = common.Hash{}
			}
		}
		if err = i.recoverLogs(subCtx, q, cursor, dedup, delivered, out); err != nil {
			return &logRecoveryError{err: err}
		}
		if *cursor > 0 {
			h, hErr := i.HeaderByNumber(subCtx, new(big.Int).SetUint64(*cursor))
			if hErr != nil {
				return &logRecoveryError{err: hErr}
			}
			if h != nil {
				*cursorHash = h.Hash()
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-sub.Err():
			if !ok {
				return fmt.Errorf("logs subscription closed")
			}
			if err == nil {
				return fmt.Errorf("logs subscription ended")
			}
			return err
		case lg := <-ch:
			if lg.Removed {
				delete(delivered, logKey(lg))
				dedup.Forget(logKey(lg))
				sendLog(ctx, out, lg)
				continue
			}
			key := logKey(lg)
			if !dedup.Seen(key) {
				if !sendLog(ctx, out, lg) {
					return ctx.Err()
				}
				delivered[key] = lg
			}
			if lg.BlockNumber > *cursor {
				*cursor = lg.BlockNumber
				*cursorHash = lg.BlockHash
				pruneDelivered(delivered, *cursor, i.cfg.Recovery.ReorgDepth)
			}
		}
	}
}

// recoverLogs 用eth_getLogs分段回填游标到当前链头之间的日志（区间过大时自适应拆分）。
func (i *Instance) recoverLogs(ctx context.Context, q ethereum.FilterQuery, cursor *uint64, dedup *subscription.Deduper, delivered map[string]types.Log, out chan<- types.Log) error {
	head, err := i.BlockNumber(ctx)
	if err != nil {
		return err
	}
	if q.ToBlock != nil && q.ToBlock.IsUint64() && q.ToBlock.Uint64() < head {
		head = q.ToBlock.Uint64()
	}
	start := *cursor + 1
	if *cursor == 0 && (q.FromBlock == nil || q.FromBlock.Sign() == 0) {
		start = 0
	}
	if head < start {
		return nil
	}
	max := i.cfg.Recovery.MaxRange
	if max == 0 {
		max = 2000
	}
	for start <= head {
		end := start + max - 1
		if end < start || end > head {
			end = head
		}
		logs, e := i.getLogsAdaptive(ctx, q, start, end)
		if e != nil {
			return e
		}
		for _, lg := range logs {
			key := logKey(lg)
			if !dedup.Seen(key) {
				if !sendLog(ctx, out, lg) {
					return ctx.Err()
				}
				delivered[key] = lg
			}
		}
		*cursor = end
		pruneDelivered(delivered, *cursor, i.cfg.Recovery.ReorgDepth)
		start = end + 1
	}
	return nil
}

// emitOrphanedLogs 在检测到重组后，把近期已投递但不再属于规范链的日志以
// 注意：Removed=true 重新投递，让索引方可以回退状态。
func (i *Instance) emitOrphanedLogs(ctx context.Context, q ethereum.FilterQuery, from, to uint64, dedup *subscription.Deduper, delivered map[string]types.Log, out chan<- types.Log) error {
	if from > to {
		return nil
	}
	canonical := make(map[string]struct{})
	max := i.cfg.Recovery.MaxRange
	if max == 0 {
		max = 2000
	}
	for start := from; start <= to; {
		end := start + max - 1
		if end < start || end > to {
			end = to
		}
		logs, err := i.getLogsAdaptive(ctx, q, start, end)
		if err != nil {
			return err
		}
		for _, lg := range logs {
			canonical[logKey(lg)] = struct{}{}
		}
		if end == to {
			break
		}
		start = end + 1
	}
	for key, lg := range delivered {
		if lg.BlockNumber < from || lg.BlockNumber > to {
			continue
		}
		if _, ok := canonical[key]; !ok {
			lg.Removed = true
			if !sendLog(ctx, out, lg) {
				return ctx.Err()
			}
			delete(delivered, key)
			dedup.Forget(key)
		}
	}
	return nil
}

// pruneDelivered 依据当前游标和重组深度，淘汰已投递map中过于陈旧的日志。
func pruneDelivered(logs map[string]types.Log, cursor, depth uint64) {
	if cursor <= depth {
		return
	}
	cutoff := cursor - depth
	for key, lg := range logs {
		if lg.BlockNumber < cutoff {
			delete(logs, key)
		}
	}
}

// logSubscriptionArg 把FilterQuery转成eth_subscribe(logs)的订阅参数。
func logSubscriptionArg(q ethereum.FilterQuery) map[string]any {
	a := map[string]any{}
	if len(q.Addresses) == 1 {
		a["address"] = q.Addresses[0].Hex()
	} else if len(q.Addresses) > 1 {
		a["address"] = q.Addresses
	}
	if len(q.Topics) > 0 {
		t := make([]any, len(q.Topics))
		for x, g := range q.Topics {
			if len(g) == 1 {
				t[x] = g[0].Hex()
			} else if len(g) > 1 {
				t[x] = g
			}
		}
		a["topics"] = t
	}
	return a
}

// logKey 生成日志去重键（blockHash/txHash/logIndex）。
func logKey(l types.Log) string {
	return fmt.Sprintf("%s/%s/%d", l.BlockHash.Hex(), l.TxHash.Hex(), l.Index)
}

// sendLog 在ctx取消前把日志写入输出通道；写入失败（ctx 取消）返回false。
func sendLog(ctx context.Context, out chan<- types.Log, l types.Log) bool {
	select {
	case out <- l:
		return true
	case <-ctx.Done():
		return false
	}
}
