package metrics

// 提供一个轻量的Prometheus文本格式暴露(不强加进程级prometheus registry给宿主应用)
// 监控指标体系：
// 1、RPC服务商级别：
//       1、该供应商当前最新的健康状态
//       2、该供应商当前最新区块
//       3、该供应商的延迟秒数
//       4、该供应商的在途请求数
//       5、该供应商的累计失败数
//       6、该供应商的累计一致性故障数
// 2、请求级别：
//       1、该请求的次数
//       2、该请求的错误次数
//       3、该请求的重试次数
//       4、该请求延迟的纳秒数
//       5、该请求的延迟次数
// 监控指标注入：
// 1、在executor层，链交互处收口处注入

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// 调用一次RPC的唯一标识 [链,提供商,方法,结果] outcome=success/error/rpc_error/retry
type key struct{ chain, provider, method, outcome string }

// 提供服务的供应商唯一标识 [链,提供商,传输层类别] transport=http/websocket
type providerKey struct{ chain, provider, transport string }

// 服务供应商状态
type providerState struct {
	healthy             float64 // 健康状态
	latestBlock         uint64  // 最新区块
	latencySeconds      float64 // 延迟秒数
	inflight            int64   // 在途请求数
	totalFailures       uint64  // 累计失败数
	consistencyFailures int     // 一致性故障累计数
}

// Counters 是一组按label聚合的原子计数器。
type Counters struct {
	Requests, Errors, Retries atomic.Uint64 // 请求次数 错误次数 重试次数
	LatencyNanos              atomic.Uint64 // 延迟纳秒
	LatencyCount              atomic.Uint64 // 延迟次数
}

// Registry 注册监控指标体系 保存RPC调用计数器与提供商状态，供文本格式输出。
type Registry struct {
	mu        sync.RWMutex
	values    map[key]*Counters             // 每次RPC的统计计数器
	providers map[providerKey]providerState // 每个服务供应商的统计计数器
}

// New 创建空的指标注册表。
func New() *Registry {
	return &Registry{values: make(map[key]*Counters), providers: make(map[providerKey]providerState)}
}

// get 通过RPC请求key获取该请求的计数器信息
func (r *Registry) get(k key) *Counters {
	r.mu.RLock()
	v := r.values[k]
	r.mu.RUnlock()
	if v != nil {
		return v
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if v = r.values[k]; v == nil {
		v = &Counters{}
		r.values[k] = v
	}
	return v
}

// Observe 记录一次RPC调用的结果（outcome 为 success/error/rpc_error 等）。
func (r *Registry) Observe(chain, provider, method, outcome string, ns uint64) {
	c := r.get(key{chain, provider, method, outcome})
	c.Requests.Add(1)
	if outcome != "success" {
		c.Errors.Add(1)
	}
	c.LatencyNanos.Add(ns)
	c.LatencyCount.Add(1)
}

// Retry 记录一次RPC调用的重试。
func (r *Registry) Retry(chain, provider, method string) {
	r.get(key{chain, provider, method, "retry"}).Retries.Add(1)
}

// SetProvider 记录一次服务提供商的统计状态  更新某提供商的最新状态快照。
func (r *Registry) SetProvider(chain, provider, transport, status string, latestBlock uint64, latencyNanos uint64, inflight int64, totalFailures uint64, consistencyFailures int) {
	healthy := float64(0)
	if status == "healthy" {
		healthy = 1
	}
	r.mu.Lock()
	r.providers[providerKey{chain, provider, transport}] = providerState{healthy: healthy, latestBlock: latestBlock, latencySeconds: float64(latencyNanos) / 1e9, inflight: inflight, totalFailures: totalFailures, consistencyFailures: consistencyFailures}
	r.mu.Unlock()
}

// esc 转义 label 中的反斜杠、引号与换行，避免破坏 Prometheus 文本格式。
func esc(s string) string {
	return strings.NewReplacer(`\\`, `\\\\`, `"`, `\\"`, `\n`, `\\n`).Replace(s)
}

// ServeHTTP 以 Prometheus 文本格式输出全部指标。
func (r *Registry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	r.mu.RLock()
	keys := make([]key, 0, len(r.values))
	for k := range r.values {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })
	vals := make(map[key]*Counters, len(keys))
	for _, k := range keys {
		vals[k] = r.values[k]
	}
	pkeys := make([]providerKey, 0, len(r.providers))
	for k := range r.providers {
		pkeys = append(pkeys, k)
	}
	sort.Slice(pkeys, func(i, j int) bool { return fmt.Sprint(pkeys[i]) < fmt.Sprint(pkeys[j]) })
	pvals := make(map[providerKey]providerState, len(pkeys))
	for _, k := range pkeys {
		pvals[k] = r.providers[k]
	}
	r.mu.RUnlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintln(w, "# TYPE chain_rpc_requests_total counter\n# TYPE chain_rpc_errors_total counter\n# TYPE chain_rpc_retries_total counter\n# TYPE chain_rpc_latency_seconds summary\n# TYPE chain_provider_healthy gauge\n# TYPE chain_provider_latest_block gauge\n# TYPE chain_provider_latency_seconds gauge\n# TYPE chain_provider_in_flight gauge\n# TYPE chain_provider_failures_total counter\n# TYPE chain_provider_consistency_failures gauge")
	for _, k := range keys {
		c := vals[k]
		labels := fmt.Sprintf(`chain="%s",provider="%s",method="%s",outcome="%s"`, esc(k.chain), esc(k.provider), esc(k.method), esc(k.outcome))
		fmt.Fprintf(w, "chain_rpc_requests_total{%s} %d\n", labels, c.Requests.Load())
		fmt.Fprintf(w, "chain_rpc_errors_total{%s} %d\n", labels, c.Errors.Load())
		fmt.Fprintf(w, "chain_rpc_retries_total{%s} %d\n", labels, c.Retries.Load())
		n := c.LatencyCount.Load()
		if n > 0 {
			fmt.Fprintf(w, "chain_rpc_latency_seconds_sum{%s} %.9f\n", labels, float64(c.LatencyNanos.Load())/1e9)
			fmt.Fprintf(w, "chain_rpc_latency_seconds_count{%s} %d\n", labels, n)
		}
	}
	for _, k := range pkeys {
		v := pvals[k]
		labels := fmt.Sprintf(`chain="%s",provider="%s",transport="%s"`, esc(k.chain), esc(k.provider), esc(k.transport))
		fmt.Fprintf(w, "chain_provider_healthy{%s} %.0f\n", labels, v.healthy)
		fmt.Fprintf(w, "chain_provider_latest_block{%s} %d\n", labels, v.latestBlock)
		fmt.Fprintf(w, "chain_provider_latency_seconds{%s} %.9f\n", labels, v.latencySeconds)
		fmt.Fprintf(w, "chain_provider_in_flight{%s} %d\n", labels, v.inflight)
		fmt.Fprintf(w, "chain_provider_failures_total{%s} %d\n", labels, v.totalFailures)
		fmt.Fprintf(w, "chain_provider_consistency_failures{%s} %d\n", labels, v.consistencyFailures)
	}
}

// Handler 返回指标 HTTP 处理器（即 Registry 自身）。
func (r *Registry) Handler() http.Handler { return r }
