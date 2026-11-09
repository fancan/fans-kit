package chain

// 统一的执行层 负责对以太坊链的底层操作收口
// 这里把与链的交互收口，在收口处做重试、故障切换、监控指标收集

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/fancan/fans-kit/chain/provider"
)

// Call 对链发起一次RPC调用：按策略选择提供商，失败时依据方法策略进行
// 重试与故障切换。应用级JSON-RPC 错误（如 revert）不会误伤熔断器。
func (i *Instance) Call(ctx context.Context, result any, method string, args ...any) error {
	if i.ctx.Err() != nil {
		return errors.New("chain instance is closed")
	}
	ctx, cleanup := i.bindContext(ctx)
	defer cleanup()
	policy := DefaultMethodPolicy(method, i.cfg.HTTP.MaxRetries)
	maxLag := uint64(0)
	if i.cfg.Block.Enabled {
		maxLag = i.cfg.Block.MaxLag
	}
	candidates := i.httpPool.Select(maxLag)
	if len(candidates) == 0 {
		return fmt.Errorf("chain %s: no available HTTP provider", i.name)
	}
	var last error
	attempts := 0
	for _, ep := range candidates {
		if attempts > policy.MaxRetries {
			break
		}
		if !ep.Acquire(time.Now()) {
			continue
		}
		attempts++
		callCtx := ctx
		cancel := func() {}
		if policy.Timeout > 0 {
			callCtx, cancel = context.WithTimeout(ctx, policy.Timeout)
		} else {
			callCtx, cancel = context.WithTimeout(ctx, i.cfg.HTTP.Timeout.Value())
		}
		start := time.Now()
		var raw json.RawMessage
		err := ep.Call(callCtx, &raw, method, args...)
		lat := time.Since(start)
		cancel()
		if err == nil {
			i.recordHTTP(ep, nil, lat)
			i.metrics.Observe(i.name, ep.ID(), method, "success", uint64(lat))
			if result != nil {
				return json.Unmarshal(raw, result)
			}
			return nil
		}
		last = err
		if isLocalRequestError(err) {
			ep.ReleaseProbe()
			return err
		}
		if errors.Is(err, context.Canceled) {
			ep.ReleaseProbe()
			return err
		}
		providerFailure := (!isRPCResponseError(err) || retryableError(err)) && !isLogRangeLimitError(err)
		if providerFailure {
			i.recordHTTP(ep, err, lat)
			i.metrics.Observe(i.name, ep.ID(), method, "error", uint64(lat))
		} else {
			i.recordHTTP(ep, nil, lat)
			i.metrics.Observe(i.name, ep.ID(), method, "rpc_error", uint64(lat))
			if !rpcMethodUnavailable(err) || !policy.Failover {
				return err
			}
		}
		if !policy.Failover || !policy.Retryable || (!retryableError(err) && !rpcMethodUnavailable(err)) {
			return err
		}
		i.metrics.Retry(i.name, ep.ID(), method)
	}
	if last == nil {
		last = errors.New("no provider attempted")
	}
	return fmt.Errorf("chain %s rpc %s failed: %w", i.name, method, last)
}

// CallRaw 对链发起一次原始RPC调用并返回未解码的JSON字节。
func (i *Instance) CallRaw(ctx context.Context, method string, args ...any) (json.RawMessage, error) {
	var out json.RawMessage
	err := i.Call(ctx, &out, method, args...)
	return out, err
}

// BatchElem 是批处理中的一个元素：Method/Args 为入参，Result 为出参，Error 记录该元素的错误。
type BatchElem struct {
	Method string
	Args   []any
	Result any
	Error  error
}

// Batch 对链发起一次JSON-RPC批处理。批处理整体发往单个提供商，不跨提供商拆分；
// 仅当批内所有方法都可安全重试时才允许传输层故障切换。
func (i *Instance) Batch(ctx context.Context, elems []BatchElem) error {
	if i.ctx.Err() != nil {
		return errors.New("chain instance is closed")
	}
	ctx, cleanup := i.bindContext(ctx)
	defer cleanup()
	if len(elems) == 0 {
		return nil
	}
	if len(elems) > i.cfg.HTTP.MaxBatchSize {
		return fmt.Errorf("batch size %d exceeds max %d", len(elems), i.cfg.HTTP.MaxBatchSize)
	}
	maxLag := uint64(0)
	if i.cfg.Block.Enabled {
		maxLag = i.cfg.Block.MaxLag
	}
	eps := i.httpPool.Select(maxLag)
	if len(eps) == 0 {
		return errors.New("no available HTTP provider")
	}
	var last error
	safeToRetry := true
	maxRetries := i.cfg.HTTP.MaxRetries
	for _, elem := range elems {
		policy := DefaultMethodPolicy(elem.Method, i.cfg.HTTP.MaxRetries)
		if !policy.Failover || !policy.Retryable {
			safeToRetry = false
			break
		}
		if policy.MaxRetries < maxRetries {
			maxRetries = policy.MaxRetries
		}
	}
	attempts := 0
	for _, ep := range eps {
		if attempts > maxRetries {
			break
		}
		if !ep.Acquire(time.Now()) {
			continue
		}
		attempts++
		for j := range elems {
			elems[j].Error = nil
		}
		raw := make([]json.RawMessage, len(elems))
		rpcElems := make([]rpc.BatchElem, len(elems))
		for j, e := range elems {
			rpcElems[j] = rpc.BatchElem{Method: e.Method, Args: e.Args, Result: &raw[j]}
		}
		ctx2, cancel := context.WithTimeout(ctx, i.cfg.HTTP.Timeout.Value())
		start := time.Now()
		err := ep.Batch(ctx2, rpcElems)
		lat := time.Since(start)
		cancel()
		if err == nil {
			for j := range rpcElems {
				if rpcElems[j].Error != nil {
					elems[j].Error = rpcElems[j].Error
					continue
				}
				if elems[j].Result != nil {
					elems[j].Error = json.Unmarshal(raw[j], elems[j].Result)
				}
			}
			i.recordHTTP(ep, nil, lat)
			i.metrics.Observe(i.name, ep.ID(), "batch", "success", uint64(lat))
			return nil
		}
		last = err
		if isLocalRequestError(err) {
			ep.ReleaseProbe()
			return err
		}
		if errors.Is(err, context.Canceled) {
			ep.ReleaseProbe()
			return err
		}
		i.recordHTTP(ep, err, lat)
		i.metrics.Observe(i.name, ep.ID(), "batch", "error", uint64(lat))
		// 批处理的顶层错误必然是传输层失败（go-ethereum 仅返回 I/O 错误），
		// 因此只要批内所有方法都可安全重试，就应故障切换到下一个提供商，
		// 不应再用 retryableError 的字符串启发式去限制切换。
		if !safeToRetry || attempts > maxRetries {
			break
		}
		i.metrics.Retry(i.name, ep.ID(), "batch")
	}
	if last == nil {
		return errors.New("batch rpc failed: no provider could be acquired")
	}
	return fmt.Errorf("batch rpc failed: %w", last)
}

// CallArg 是一次 eth_call 的参数（仅普通 RPC，无需依赖 multicall 合约）。
type CallArg struct {
	To   common.Address
	Data []byte
}

// errBatchRejected 标记「网关整体拒绝的批次」：例如某些提供商对每个元素都返回限流错误。
var errBatchRejected = errors.New("chain: batch rejected by provider")

// BatchCall 在单个JSON-RPC批处理中执行多个eth_call。
// 结果与输入一一对应；单个元素revert是合法结果，对应位置返回nil空条目；
// 仅当网关整体拒绝（全部元素为限流类错误）或传输层失败时才故障切换到下一个提供商。
func (i *Instance) BatchCall(ctx context.Context, calls []CallArg) ([][]byte, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	if i.ctx.Err() != nil {
		return nil, errors.New("chain instance is closed")
	}
	ctx, cleanup := i.bindContext(ctx)
	defer cleanup()
	if len(calls) > i.cfg.HTTP.MaxBatchSize {
		return nil, fmt.Errorf("batch size %d exceeds max %d", len(calls), i.cfg.HTTP.MaxBatchSize)
	}

	maxLag := uint64(0)
	if i.cfg.Block.Enabled {
		maxLag = i.cfg.Block.MaxLag
	}
	eps := i.httpPool.Select(maxLag)
	if len(eps) == 0 {
		return nil, errors.New("no available HTTP provider")
	}

	var last [][]byte
	var lastErr error
	maxRetries := i.cfg.HTTP.MaxRetries
	attempts := 0
	for _, ep := range eps {
		if attempts > maxRetries {
			break
		}
		if !ep.Acquire(time.Now()) {
			continue
		}
		attempts++

		// 与Batch一致：每个尝试都套上HTTP超时，避免单个请求挂死。
		callCtx, cancel := context.WithTimeout(ctx, i.cfg.HTTP.Timeout.Value())
		start := time.Now()
		out, err := i.batchCallOnce(callCtx, ep, calls)
		lat := time.Since(start)
		cancel()

		if err == nil {
			i.recordHTTP(ep, nil, lat)
			i.metrics.Observe(i.name, ep.ID(), "batch_call", "success", uint64(lat))
			return out, nil
		}

		last, lastErr = out, err
		if ctx.Err() != nil {
			ep.ReleaseProbe()
			return nil, ctx.Err()
		}
		if isLocalRequestError(err) {
			ep.ReleaseProbe()
			return nil, err
		}

		i.recordHTTP(ep, err, lat)
		i.metrics.Observe(i.name, ep.ID(), "batch_call", "error", uint64(lat))

		// 传输失败或网关整体拒绝都应故障切换；其它错误（理论上不会出现）直接返回。
		if attempts > maxRetries {
			break
		}
		i.metrics.Retry(i.name, ep.ID(), "batch_call")
	}

	if lastErr == nil {
		return nil, errors.New("batch call failed: no provider could be acquired")
	}
	return last, lastErr
}

// batchCallOnce 对单个端点执行一次批量 eth_call。
// 返回 nil error 时，out 中 revert/失败元素为 nil；返回 errBatchRejected 表示网关整体拒绝。
func (i *Instance) batchCallOnce(ctx context.Context, ep *provider.Endpoint, calls []CallArg) ([][]byte, error) {
	out := make([][]byte, len(calls))
	raw := make([]json.RawMessage, len(calls))
	elems := make([]rpc.BatchElem, len(calls))
	for idx, ca := range calls {
		elems[idx] = rpc.BatchElem{
			Method: "eth_call",
			Args: []any{
				map[string]any{"to": ca.To.Hex(), "data": "0x" + hex.EncodeToString(ca.Data)},
				"latest",
			},
			Result: &raw[idx],
		}
	}

	if err := ep.Batch(ctx, elems); err != nil {
		return nil, err
	}

	successes, providerErrs, otherErrs := 0, 0, 0
	for idx := range calls {
		if elems[idx].Error != nil {
			// revert 等应用级错误属于 otherErrs（合法结果，返回 nil 空条目）；
			// 限流/5xx 等提供商级错误属于 providerErrs。
			if retryableError(elems[idx].Error) {
				providerErrs++
			} else {
				otherErrs++
			}
			continue
		}
		b, err := hex.DecodeString(strings.TrimPrefix(string(raw[idx]), "0x"))
		if err != nil {
			continue
		}
		out[idx] = b
		successes++
	}

	// 网关整体拒绝（无成功、有提供商错误、无其它错误）→ 视为 provider 级失败，触发故障切换。
	if successes == 0 && providerErrs > 0 && otherErrs == 0 {
		return nil, fmt.Errorf("%w (%d elements)", errBatchRejected, providerErrs)
	}
	return out, nil
}
