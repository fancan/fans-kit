package chain

// 故障切换策略层

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

// MethodPolicy 描述某个 RPC 方法的重试/故障切换策略。
type MethodPolicy struct {
	Retryable  bool          // 是否允许重试
	Failover   bool          // 是否允许切换到其他提供商
	MaxRetries int           // 最大重试次数
	Timeout    time.Duration // 单次调用超时（0 表示使用全局HTTP超时）
}

var methodPolicies = struct {
	sync.RWMutex
	values map[string]MethodPolicy
}{values: make(map[string]MethodPolicy)}

// RegisterMethodPolicy 为某个精确的RPC方法注册切换策略。
func RegisterMethodPolicy(method string, policy MethodPolicy) {
	methodPolicies.Lock()
	methodPolicies.values[method] = policy
	methodPolicies.Unlock()
}

// UnregisterMethodPolicy 移除某个方法的切换策略。
func UnregisterMethodPolicy(method string) {
	methodPolicies.Lock()
	delete(methodPolicies.values, method)
	methodPolicies.Unlock()
}

// DefaultMethodPolicy 返回某方法的默认切换策略：
// 读方法默认可重试/可切换；eth_send*/personal_/miner_ 等写方法默认不重放；
// debug_/trace_ 等重方法限制最多重试1次。
func DefaultMethodPolicy(method string, max int) MethodPolicy {
	methodPolicies.RLock()
	override, ok := methodPolicies.values[method]
	methodPolicies.RUnlock()
	if ok {
		if override.MaxRetries < 0 {
			override.MaxRetries = 0
		}
		return override
	}
	p := MethodPolicy{Retryable: true, Failover: true, MaxRetries: max}
	switch {
	case strings.HasPrefix(method, "eth_send"), strings.HasPrefix(method, "personal_"), strings.HasPrefix(method, "miner_"):
		p.Retryable = false
		p.Failover = false
		p.MaxRetries = 0
	case strings.HasPrefix(method, "debug_"), strings.HasPrefix(method, "trace_"):
		p.MaxRetries = min(max, 1)
	}
	return p
}

// retryableSubstrings 是判定“可重试”传输错误的子串集合
var retryableSubstrings = []string{"connection refused", "connection reset", "broken pipe", "i/o timeout", "timeout", "temporarily unavailable", "too many requests", "http 408", "http 429", "http 500", "http 502", "http 503", "http 504", "status 408", "status 429", "status 500", "status 502", "status 503", "status 504", "eof", "server misbehaving", "rate limit", "limit exceeded", "-32005"}

// retryableError 判断一个传输层错误是否值得重试（限流、超时、连接重置、5xx 等）。
func retryableError(err error) bool {
	if err == nil {
		return false
	}
	if code, ok := rpcErrorCode(err); ok && (code == -32005 || code == -32016) {
		return true
	}
	if ne, ok := err.(net.Error); ok {
		return ne.Timeout() || ne.Temporary()
	}
	s := strings.ToLower(err.Error())
	for _, v := range retryableSubstrings {
		if strings.Contains(s, v) {
			return true
		}
	}
	return false
}

// isRPCResponseError 区分 JSON-RPC 应用级错误与传输层失败。
// revert 或非法参数等响应证明端点可达，不应触发熔断器。
func isRPCResponseError(err error) bool {
	if err == nil {
		return false
	}
	_, ok := rpcErrorCode(err)
	return ok
}

// rpcMethodUnavailable 判断错误是否为“方法不存在”（错误码 -32601）。
func rpcMethodUnavailable(err error) bool {
	if err == nil {
		return false
	}
	code, ok := rpcErrorCode(err)
	return ok && code == -32601
}

// isLocalRequestError 判断错误是否来自本地参数编解码（如结果参数非指针），
// 这类错误无需重试，应立即返回。
func isLocalRequestError(err error) bool {
	if err == nil {
		return false
	}
	var unsupportedType *json.UnsupportedTypeError
	var unsupportedValue *json.UnsupportedValueError
	var marshaler *json.MarshalerError
	if errors.As(err, &unsupportedType) || errors.As(err, &unsupportedValue) || errors.As(err, &marshaler) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "call result parameter must be pointer") || strings.Contains(s, "json: unsupported")
}

// rpcErrorCode 提取 go-ethereum rpc 错误的 ErrorCode（若有）。
func rpcErrorCode(err error) (int, bool) {
	var rpcErr interface {
		error
		ErrorCode() int
	}
	if errors.As(err, &rpcErr) {
		return rpcErr.ErrorCode(), true
	}
	return 0, false
}
