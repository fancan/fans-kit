package chain

// 监控provider

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/fancan/fans-kit/chain/provider"
)

// monitorLoop 周期执行提供商健康检查与区块监控，直到实例关闭。
func (i *Instance) monitorLoop() {
	defer i.wg.Done()
	interval := i.cfg.Health.Interval.Value()
	if !i.cfg.Health.Enabled {
		interval = i.cfg.Block.Interval.Value()
	} else if i.cfg.Block.Enabled {
		interval = minDuration(interval, i.cfg.Block.Interval.Value())
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-i.stop:
			return
		case <-ticker.C:
			i.checkProviders()
		}
	}
}

// minDuration 返回两者中的较小值（忽略非正值）。
func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

// checkProviders 对 HTTP/WS 池逐端点做链 ID 与区块号探测，刷新状态与区块高度；
// 若启用区块监控，再比较各提供商在共同高度上的区块哈希一致性。
func (i *Instance) checkProviders() {
	for _, e := range i.httpPool.All() {
		if !e.Acquire(time.Now()) {
			continue
		}
		ctx, cancel := context.WithTimeout(i.ctx, i.cfg.Health.Timeout.Value())
		start := time.Now()
		var id hexutil.Uint64
		err := e.Call(ctx, &id, "eth_chainId")
		if err == nil && i.cfg.ChainID != 0 && uint64(id) != i.cfg.ChainID {
			err = fmt.Errorf("chain id mismatch: got %d want %d", id, i.cfg.ChainID)
		}
		var block hexutil.Uint64
		if err == nil {
			err = e.Call(ctx, &block, "eth_blockNumber")
		}
		lat := time.Since(start)
		cancel()
		if err == nil {
			e.SetBlock(uint64(block))
		}
		i.recordHTTP(e, err, lat)
	}
	for _, e := range i.wsPool.All() {
		if !e.Acquire(time.Now()) {
			continue
		}
		ctx, cancel := context.WithTimeout(i.ctx, i.cfg.Health.Timeout.Value())
		start := time.Now()
		var id hexutil.Uint64
		err := e.CallWS(ctx, &id, "eth_chainId")
		if err == nil && i.cfg.ChainID != 0 && uint64(id) != i.cfg.ChainID {
			err = fmt.Errorf("chain id mismatch: got %d want %d", id, i.cfg.ChainID)
		}
		var block hexutil.Uint64
		if err == nil {
			err = e.CallWS(ctx, &block, "eth_blockNumber")
		}
		lat := time.Since(start)
		cancel()
		if err == nil {
			e.SetBlock(uint64(block))
		}
		i.recordWS(e, err, lat)
	}
	if i.cfg.Block.Enabled {
		i.checkPoolConsistency(i.httpPool, false)
		i.checkPoolConsistency(i.wsPool, true)
	}
}

// checkPoolConsistency 在共同的“近似最终”高度比较各提供商的区块哈希。
// 仅当严格多数不一致时才对少数派端点记一次一致性失败；票数相持视为模糊、不做惩罚。
func (i *Instance) checkPoolConsistency(pool *provider.Pool, websocket bool) {
	all := pool.All()
	if len(all) < 2 {
		return
	}
	now := time.Now()
	available := make([]*provider.Endpoint, 0, len(all))
	maxHeight := uint64(0)
	for _, ep := range all {
		if !ep.Available(now) {
			continue
		}
		available = append(available, ep)
		s := ep.Snapshot()
		if s.Status != provider.Open && s.LatestBlock > maxHeight {
			maxHeight = s.LatestBlock
		}
	}
	if maxHeight == 0 {
		for _, ep := range available {
			if b := ep.Snapshot().LatestBlock; b > maxHeight {
				maxHeight = b
			}
		}
	}
	if maxHeight == 0 {
		return
	}
	minHeight := ^uint64(0)
	eligible := make([]*provider.Endpoint, 0, len(available))
	for _, ep := range available {
		s := ep.Snapshot()
		if s.LatestBlock == 0 {
			continue
		}
		if s.LatestBlock < maxHeight && maxHeight-s.LatestBlock > i.cfg.Block.MaxLag {
			continue
		}
		if !ep.Acquire(now) {
			continue
		}
		eligible = append(eligible, ep)
		if s.LatestBlock < minHeight {
			minHeight = s.LatestBlock
		}
	}
	if len(eligible) < 2 || minHeight == ^uint64(0) {
		for _, ep := range eligible {
			ep.ReleaseProbe()
		}
		return
	}
	depth := i.cfg.Block.ConsistencyDepth
	if minHeight > depth {
		minHeight -= depth
	} else {
		minHeight = 0
	}
	type answer struct {
		ep      *provider.Endpoint
		hash    common.Hash
		latency time.Duration
		err     error
	}
	answers := make([]answer, 0, len(eligible))
	counts := make(map[common.Hash]int)
	for _, ep := range eligible {
		ctx, cancel := context.WithTimeout(i.ctx, i.cfg.Health.Timeout.Value())
		start := time.Now()
		var h *types.Header
		blockArg := hexutil.EncodeUint64(minHeight)
		var err error
		if websocket {
			err = ep.CallWS(ctx, &h, "eth_getBlockByNumber", blockArg, false)
		} else {
			err = ep.Call(ctx, &h, "eth_getBlockByNumber", blockArg, false)
		}
		lat := time.Since(start)
		cancel()
		a := answer{ep: ep, latency: lat, err: err}
		if err == nil && h != nil {
			a.hash = h.Hash()
			counts[a.hash]++
		} else if err == nil {
			a.err = fmt.Errorf("empty block header at %d", minHeight)
		}
		answers = append(answers, a)
	}
	var majority common.Hash
	majorityCount, total := 0, 0
	for h, n := range counts {
		total += n
		if n > majorityCount {
			majority, majorityCount = h, n
		}
	}
	if total < 2 || majorityCount <= total/2 {
		for _, a := range answers {
			a.ep.ReleaseProbe()
		}
		return
	}
	for _, a := range answers {
		var consistencyErr error
		if a.err != nil {
			consistencyErr = a.err
		} else if a.hash != majority {
			consistencyErr = fmt.Errorf("block hash mismatch at height %d", minHeight)
		}
		pool.RecordConsistency(a.ep, consistencyErr)
		s := a.ep.Snapshot()
		i.metrics.SetProvider(i.name, s.ID, s.Transport, s.Status.String(), s.LatestBlock, uint64(s.Latency), s.InFlight, s.TotalFailures, s.ConsistencyFailures)
	}
}
