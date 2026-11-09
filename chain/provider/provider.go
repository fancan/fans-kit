package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/fancan/fans-kit/chain/config"
	"github.com/fancan/fans-kit/chain/transport"
)

// ConnectionError 封装连接错误；Error()中刻意不包含端点URL（可能含有API Key）。
type ConnectionError struct {
	Provider, Transport string
	Cause               error
}

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("provider %s %s connection failed", e.Provider, e.Transport)
}
func (e *ConnectionError) Unwrap() error { return e.Cause }

// Status 是端点健康状态。
type Status uint8

const (
	Healthy  Status = iota // 健康
	Degraded               // 降级（偶发失败）
	Open                   // 熔断打开（不可用）
	HalfOpen               // 半开（试探中）
)

// Status类型状态转string
func (s Status) String() string {
	switch s {
	case Healthy:
		return "healthy"
	case Degraded:
		return "degraded"
	case Open:
		return "open"
	case HalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// Endpoint 是一个RPC端点（对应一个提供商的HTTP或WS地址），
// 内含熔断状态、延迟、区块高度与在途请求数等运行时信息。
type Endpoint struct {
	cfg                  config.ProviderConfig // 端点配置
	httpClient, wsClient *transport.Client     // 传输层客户端
	connMu               sync.Mutex            // 连接锁
	mu                   sync.RWMutex          // 锁
	status               Status                // 端点状态
	probeInFlight        bool                  // 传输中的探测包
	failures, successes  int                   // 成功累计数 失败累积数
	consistencyFailures  int                   // 一致性故障次数
	totalFailures        uint64                // 失败累计数
	openUntil            time.Time             // 开放至xxx
	latestBlock          uint64                // 最新区块
	latency              time.Duration         // 延迟秒数
	lastErr              error                 // 最新错误
	inflight             atomic.Int64          // 在途请求数
}

// NewEndpoint 拨号创建端点：HTTP必需成功；WS失败时标记为降级但保留端点（后续可重连）。
func NewEndpoint(ctx context.Context, c config.ProviderConfig) (*Endpoint, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	e := &Endpoint{cfg: c, status: Healthy}
	h := make(http.Header)
	for k, v := range c.Headers {
		h.Set(k, v)
	}
	if c.HTTPURL != "" {
		x, err := transport.Dial(ctx, c.HTTPURL, h)
		if err != nil {
			return nil, &ConnectionError{Provider: c.Name, Transport: "http", Cause: err}
		}
		e.httpClient = x
	}
	if c.WSURL != "" {
		x, err := transport.Dial(ctx, c.WSURL, h)
		if err != nil {
			e.status = Degraded
			e.lastErr = &ConnectionError{Provider: c.Name, Transport: "websocket", Cause: err}
		} else {
			e.wsClient = x
		}
	}
	return e, nil
}

// ensureWS 返回可用的WS客户端；已关闭或未建立时按需重连。
func (e *Endpoint) ensureWS(ctx context.Context) (*transport.Client, error) {
	e.connMu.Lock()
	defer e.connMu.Unlock()
	if e.wsClient != nil && !e.wsClient.Closed() {
		return e.wsClient, nil
	}
	if e.cfg.WSURL == "" {
		return nil, errors.New("websocket endpoint unavailable")
	}
	h := make(http.Header)
	for k, v := range e.cfg.Headers {
		h.Set(k, v)
	}
	c, err := transport.Dial(ctx, e.cfg.WSURL, h)
	if err != nil {
		return nil, &ConnectionError{Provider: e.cfg.Name, Transport: "websocket", Cause: err}
	}
	e.wsClient = c
	return c, nil
}

// ID 返回提供商名称。
func (e *Endpoint) ID() string { return e.cfg.Name }

// Priority 返回优先级。
func (e *Endpoint) Priority() int { return e.cfg.Priority }

// Weight 返回权重。
func (e *Endpoint) Weight() int { return e.cfg.Weight }

// HasHTTP 返回是否具备HTTP客户端。
func (e *Endpoint) HasHTTP() bool { return e.httpClient != nil }

// HasWS 返回是否具备HTTP客户端。
func (e *Endpoint) HasWS() bool { return e.wsClient != nil }

// HasWSConf 返回是否配置了WS地址。
func (e *Endpoint) HasWSConf() bool { return e.cfg.WSURL != "" }

// Call 通过HTTP发起一次RPC调用（维护在途请求计数）。
func (e *Endpoint) Call(ctx context.Context, result any, method string, args ...any) error {
	if e.httpClient == nil {
		return errors.New("http endpoint unavailable")
	}
	e.inflight.Add(1)
	defer e.inflight.Add(-1)
	return e.httpClient.Call(ctx, result, method, args...)
}

// CallWS 通过WebSocket发起一次RPC调用（按需建立连接）。
func (e *Endpoint) CallWS(ctx context.Context, result any, method string, args ...any) error {
	c, err := e.ensureWS(ctx)
	if err != nil {
		return err
	}
	e.inflight.Add(1)
	defer e.inflight.Add(-1)
	return c.Call(ctx, result, method, args...)
}

// Batch 通过HTTP发起一次 JSON-RPC 批处理。
func (e *Endpoint) Batch(ctx context.Context, elems []rpc.BatchElem) error {
	if e.httpClient == nil {
		return errors.New("http endpoint unavailable")
	}
	return e.httpClient.Batch(ctx, elems)
}

// Subscribe 通过WS建立订阅（按需建立连接）。
func (e *Endpoint) Subscribe(ctx context.Context, ns string, ch any, args ...any) (*rpc.ClientSubscription, error) {
	c, err := e.ensureWS(ctx)
	if err != nil {
		return nil, err
	}
	return c.Subscribe(ctx, ns, ch, args...)
}

// Close 关闭端点的 HTTP/WS 连接。
func (e *Endpoint) Close() {
	e.connMu.Lock()
	defer e.connMu.Unlock()
	if e.httpClient != nil {
		e.httpClient.Close()
	}
	if e.wsClient != nil {
		e.wsClient.Close()
		e.wsClient = nil
	}
}

// Snapshot 返回端点当前状态的只读快照。
func (e *Endpoint) Snapshot() Snapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	transportName := "http"
	if e.cfg.HTTPURL == "" {
		transportName = "websocket"
	}
	return Snapshot{ID: e.ID(), Transport: transportName, Priority: e.Priority(), Weight: e.Weight(), Status: e.status, Failures: e.failures, ConsistencyFailures: e.consistencyFailures, TotalFailures: e.totalFailures, Successes: e.successes, OpenUntil: e.openUntil, LatestBlock: e.latestBlock, Latency: e.latency, LastError: errorString(e.lastErr), InFlight: e.inflight.Load(), ProbeInFlight: e.probeInFlight}
}

// Snapshot 是端点健康状态的对外快照。
type Snapshot struct {
	ID                  string        `json:"id"`                   // 端点Id
	Transport           string        `json:"transport"`            // 传输层类别
	Priority            int           `json:"priority"`             // 优先级
	Weight              int           `json:"weight"`               // 权重
	Status              Status        `json:"status"`               // 状态
	Failures            int           `json:"consecutive_failures"` // 连续失败次数
	ConsistencyFailures int           `json:"consistency_failures"` // 一致性失败次数
	TotalFailures       uint64        `json:"total_failures"`       // 累计失败次数
	Successes           int           `json:"successes"`            // 成功数
	OpenUntil           time.Time     `json:"open_until"`           // 开放至xxx
	LatestBlock         uint64        `json:"latest_block"`         // 最新区块
	Latency             time.Duration `json:"latency"`              // 延迟
	LastError           string        `json:"last_error"`           // 最新错误
	InFlight            int64         `json:"in_flight"`            // 在途请求数
	ProbeInFlight       bool          `json:"-"`                    // 传输中的探测包
}

func errorString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

// Record 记录一次端点请求相关状态 依据一次调用结果后更新端点熔断状态（指数滑动平均延迟）。
func (e *Endpoint) Record(err error, lat time.Duration, failThreshold, successThreshold int, cooldown time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.latency == 0 {
		e.latency = lat
	} else if lat > 0 {
		e.latency = (e.latency*7 + lat) / 8
	}
	e.probeInFlight = false
	if err != nil {
		e.failures++
		e.totalFailures++
		e.successes = 0
		e.lastErr = err
		if e.failures >= failThreshold {
			e.status = Open
			e.openUntil = time.Now().Add(cooldown)
		} else {
			e.status = Degraded
		}
		return
	}
	e.successes++
	e.failures = 0
	if e.consistencyFailures == 0 {
		e.lastErr = nil
	}
	if e.status == Open || e.status == HalfOpen || e.status == Degraded {
		if e.successes >= successThreshold && e.consistencyFailures == 0 {
			e.status = Healthy
		}
	} else {
		e.status = Healthy
	}
}

// RecordConsistency 记录一次端点一致性相关状态 记录一次区块一致性检查结果（与普通调用失败分开计数）。
func (e *Endpoint) RecordConsistency(err error, failThreshold int, cooldown time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.probeInFlight = false
	if err != nil {
		e.consistencyFailures++
		e.totalFailures++
		e.lastErr = err
		if e.consistencyFailures >= failThreshold {
			e.status = Open
			e.openUntil = time.Now().Add(cooldown)
		} else {
			e.status = Degraded
		}
		return
	}
	e.consistencyFailures = 0
	if e.failures == 0 && e.successes > 0 {
		e.status = Healthy
		e.lastErr = nil
	}
}

// SetBlock 更新端点的最新区块号。
func (e *Endpoint) SetBlock(n uint64) { e.mu.Lock(); e.latestBlock = n; e.mu.Unlock() }

// Available 判断端点当前是否可被选中（熔断冷却期或半开试探中则不可用）。
func (e *Endpoint) Available(now time.Time) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.status == Open && now.Before(e.openUntil) {
		return false
	}
	if e.status == HalfOpen && e.probeInFlight {
		return false
	}
	return true
}

// Acquire 在实际调用前抢占唯一的半开试探槽位；失败返回 false。
func (e *Endpoint) Acquire(now time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.status == Open {
		if now.Before(e.openUntil) {
			return false
		}
		e.status = HalfOpen
	}
	if e.status == HalfOpen {
		if e.probeInFlight {
			return false
		}
		e.probeInFlight = true
	}
	return true
}

// Pool 端点池 是同一传输（HTTP或WS）下的一组端点，负责选择与记录。
type Pool struct {
	mu                              sync.RWMutex
	items                           map[string]*Endpoint
	failThreshold, successThreshold int           // 失败阈值 成功阈值
	cooldown                        time.Duration // 冷却时间
	selector                        Selector      // 端点选择器 实现不同的端点选中策略
}

// NewPool 创建端点池并设置熔断阈值、冷却时间与默认选择器。
func NewPool(ft, st int, cd time.Duration) *Pool {
	return &Pool{items: map[string]*Endpoint{}, failThreshold: ft, successThreshold: st, cooldown: cd, selector: PriorityLatencySelector{}}
}

// Add 向池中新增一个端点（同名重复则报错）。
func (p *Pool) Add(e *Endpoint) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.items[e.ID()]; ok {
		return fmt.Errorf("provider %q already exists", e.ID())
	}
	p.items[e.ID()] = e
	return nil
}

// Remove 从池中移除并关闭指定端点。
func (p *Pool) Remove(id string) error {
	p.mu.Lock()
	e := p.items[id]
	if e != nil {
		delete(p.items, id)
	}
	p.mu.Unlock()
	if e == nil {
		return fmt.Errorf("provider %q not found", id)
	}
	e.Close()
	return nil
}

// Get 按名称返回端点（不存在返回 nil）。
func (p *Pool) Get(id string) *Endpoint { p.mu.RLock(); defer p.mu.RUnlock(); return p.items[id] }

// All 返回全部端点（按优先级、名称排序）。
func (p *Pool) All() []*Endpoint {
	p.mu.RLock()
	out := make([]*Endpoint, 0, len(p.items))
	for _, e := range p.items {
		out = append(out, e)
	}
	p.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority() == out[j].Priority() {
			return out[i].ID() < out[j].ID()
		}
		return out[i].Priority() < out[j].Priority()
	})
	return out
}

// Select 选出当前可用的端点并按选择器排序；启用区块监控时剔除落后过大的端点。
// 每个端点只取一次快照，避免对同一端点反复加锁取 Snapshot / Available。
func (p *Pool) Select(maxLag uint64) []*Endpoint {
	all := p.All()
	now := time.Now()

	snaps := make([]Snapshot, len(all))
	var best uint64
	for i, e := range all {
		snaps[i] = e.Snapshot()
		if snaps[i].Status != Open && snaps[i].LatestBlock > best {
			best = snaps[i].LatestBlock
		}
	}
	if best == 0 {
		for i := range snaps {
			if snaps[i].LatestBlock > best {
				best = snaps[i].LatestBlock
			}
		}
	}

	var out []*Endpoint
	for i, e := range all {
		s := snaps[i]
		if maxLag > 0 && best > 0 && (s.LatestBlock == 0 || (s.LatestBlock < best && best-s.LatestBlock > maxLag)) {
			continue
		}
		if !snapshotAvailable(s, now) {
			continue
		}
		out = append(out, e)
	}

	p.mu.RLock()
	selector := p.selector
	p.mu.RUnlock()
	if selector != nil {
		out = selector.Order(out)
	}
	return out
}

// snapshotAvailable 依据快照判断端点是否可被选中（熔断冷却期或半开试探中则不可用）。
func snapshotAvailable(s Snapshot, now time.Time) bool {
	if s.Status == Open && now.Before(s.OpenUntil) {
		return false
	}
	if s.Status == HalfOpen && s.ProbeInFlight {
		return false
	}
	return true
}

// SetSelector 安装自定义选择器。
func (p *Pool) SetSelector(selector Selector) { p.mu.Lock(); p.selector = selector; p.mu.Unlock() }

// Record 记录一次调用结果到端点。
func (p *Pool) Record(e *Endpoint, err error, lat time.Duration) {
	e.Record(err, lat, p.failThreshold, p.successThreshold, p.cooldown)
}

// RecordConsistency 记录一次一致性检查结果到端点。
func (p *Pool) RecordConsistency(e *Endpoint, err error) {
	e.RecordConsistency(err, p.failThreshold, p.cooldown)
}

// Snapshots 返回池中全部端点的快照。
func (p *Pool) Snapshots() []Snapshot {
	all := p.All()
	out := make([]Snapshot, 0, len(all))
	for _, e := range all {
		out = append(out, e.Snapshot())
	}
	return out
}

// ReleaseProbe 在调用方取消但端点尚未返回有意义结果时释放半开槽位（不计成败）。
func (e *Endpoint) ReleaseProbe() {
	e.mu.Lock()
	e.probeInFlight = false
	e.mu.Unlock()
}
