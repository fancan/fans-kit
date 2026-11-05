package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
)

// Call 对链发起一次 RPC 调用：按策略选择提供商，失败时依据方法策略进行
// 重试与故障切换。应用级 JSON-RPC 错误（如 revert）不会误伤熔断器。
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

// CallRaw 对链发起一次原始 RPC 调用并返回未解码的 JSON 字节。
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

// Batch 对链发起一次 JSON-RPC 批处理。批处理整体发往单个提供商，不跨提供商拆分；
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
