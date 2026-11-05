// Package chain 提供一个零侵入、多链的以太坊 JSON-RPC 门面。
//
// 业务模块只需 import 本包并通过 Init / InitFile 传入配置文件即可开箱即用，
// 无需注入实例，也无需自行管理不同 RPC 提供商；底层同时支持 HTTP(S) 与
// WebSocket 两种形式的以太坊 RPC 提供商，并提供健康检查、熔断与故障自动切换。
package chain

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/fancan/fans-kit/chain/config"
	"github.com/fancan/fans-kit/chain/metrics"
	"github.com/fancan/fans-kit/chain/provider"
)

type Config = config.Config
type ChainConfig = config.ChainConfig
type ProviderConfig = config.ProviderConfig

// Manager 管理多条链实例。每条链对应一个 *Instance，内部维护独立的 HTTP/WS
// 提供商池。Manager 的方法均并发安全，可在运行期动态增删链。
type Manager struct {
	mu             sync.RWMutex
	chains         map[string]*Instance
	defaultName    string
	metrics        *metrics.Registry
	metricsEnabled bool
	metricsPath    string
}

// NewManager 依据配置构建多链管理器。
// 链实例按名称排序后依次创建，保证初始化顺序确定；任一条链创建失败都会回滚关闭
// 已创建的链并返回错误。
func NewManager(cfg Config) (*Manager, error) {
	if len(cfg.Chains) == 0 {
		return nil, errors.New("chain: no chains configured")
	}
	if cfg.Metrics.Path == "" {
		cfg.Metrics.Path = "/metrics"
	}
	if !strings.HasPrefix(cfg.Metrics.Path, "/") {
		cfg.Metrics.Path = "/" + cfg.Metrics.Path
	}
	m := &Manager{chains: map[string]*Instance{}, defaultName: cfg.Default, metrics: metrics.New(), metricsEnabled: cfg.Metrics.Enabled, metricsPath: cfg.Metrics.Path}
	names := make([]string, 0, len(cfg.Chains))
	for name := range cfg.Chains {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cc := cfg.Chains[name]
		i, err := NewInstance(name, cc, m.metrics)
		if err != nil {
			m.Close()
			return nil, err
		}
		m.chains[name] = i
	}
	if m.defaultName == "" && len(names) > 0 {
		m.defaultName = names[0]
	}
	if m.chains[m.defaultName] == nil {
		m.Close()
		return nil, fmt.Errorf("default chain %q not found", m.defaultName)
	}
	return m, nil
}

// Get 按名称返回链实例；名称不存在时返回错误。
func (m *Manager) Get(name string) (*Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	i := m.chains[name]
	if i == nil {
		return nil, fmt.Errorf("chain %q not found", name)
	}
	return i, nil
}

// Default 返回默认链实例。
func (m *Manager) Default() (*Instance, error) { return m.Get(m.defaultName) }

// AddChain 运行期动态新增一条链（双重检查，避免与并发新增冲突）。
func (m *Manager) AddChain(name string, cfg ChainConfig) error {
	m.mu.RLock()
	_, exists := m.chains[name]
	m.mu.RUnlock()
	if exists {
		return fmt.Errorf("chain %q already exists", name)
	}
	i, err := NewInstance(name, cfg, m.metrics)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists = m.chains[name]; exists {
		_ = i.Close()
		return fmt.Errorf("chain %q already exists", name)
	}
	m.chains[name] = i
	if m.defaultName == "" {
		m.defaultName = name
	}
	return nil
}

// RemoveChain 运行期移除一条链并关闭其全部连接；若移除的是默认链则重选默认链。
func (m *Manager) RemoveChain(name string) error {
	m.mu.Lock()
	i := m.chains[name]
	if i != nil {
		delete(m.chains, name)
		if m.defaultName == name {
			m.defaultName = ""
			names := make([]string, 0, len(m.chains))
			for n := range m.chains {
				names = append(names, n)
			}
			sort.Strings(names)
			if len(names) > 0 {
				m.defaultName = names[0]
			}
		}
	}
	m.mu.Unlock()
	if i == nil {
		return fmt.Errorf("chain %q not found", name)
	}
	return i.Close()
}

// MetricsHandler 返回 Prometheus 指标 HTTP 处理器；指标未启用时返回 404。
func (m *Manager) MetricsHandler() http.Handler {
	if !m.metricsEnabled {
		return http.NotFoundHandler()
	}
	return m.metrics.Handler()
}

// MetricsPath 返回指标暴露路径（例如 /metrics）。
func (m *Manager) MetricsPath() string { return m.metricsPath }

// RegisterMetrics 将当前全局管理器的指标处理器挂载到给定 mux。
func RegisterMetrics(mux *http.ServeMux) error {
	if mux == nil {
		return errors.New("metrics mux is nil")
	}
	m, err := CurrentManager()
	if err != nil {
		return err
	}
	if !m.metricsEnabled {
		return errors.New("chain metrics are disabled in configuration")
	}
	mux.Handle(m.metricsPath, m.metrics.Handler())
	return nil
}

// Close 关闭管理器并关闭其下所有链实例，聚合返回错误。
func (m *Manager) Close() error {
	m.mu.Lock()
	items := m.chains
	m.chains = map[string]*Instance{}
	m.mu.Unlock()
	var errs []error
	for _, i := range items {
		if err := i.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

var global struct {
	sync.RWMutex
	m *Manager
}

// Init 初始化全局管理器。建议进程启动时调用一次；重复调用会替换并关闭旧管理器。
func Init(cfg Config) error {
	m, err := NewManager(cfg)
	if err != nil {
		return err
	}
	global.Lock()
	old := global.m
	global.m = m
	global.Unlock()
	if old != nil {
		return old.Close()
	}
	return nil
}

// InitYAML 从 YAML 字节内容初始化全局管理器。
func InitYAML(data []byte) error {
	cfg, err := config.ParseYAML(data)
	if err != nil {
		return err
	}
	return Init(cfg)
}

// InitFile 从配置文件路径加载 YAML 并初始化全局管理器（开箱即用入口）。
func InitFile(path string) error {
	cfg, err := config.LoadYAML(path)
	if err != nil {
		return err
	}
	return Init(cfg)
}

// Close 关闭全局管理器（若已初始化）。
func Close() error {
	global.Lock()
	m := global.m
	global.m = nil
	global.Unlock()
	if m != nil {
		return m.Close()
	}
	return nil
}

// CurrentManager 返回当前全局管理器；未初始化时返回错误。
func CurrentManager() (*Manager, error) {
	global.RLock()
	defer global.RUnlock()
	if global.m == nil {
		return nil, errors.New("chain: not initialized; call chain.Init first")
	}
	return global.m, nil
}
func current() (*Instance, error) {
	m, e := CurrentManager()
	if e != nil {
		return nil, e
	}
	return m.Default()
}

// On 按名称返回链实例（零注入门面：自动使用全局管理器）。
func On(name string) (*Instance, error) {
	m, e := CurrentManager()
	if e != nil {
		return nil, e
	}
	return m.Get(name)
}
func MetricsHandler() (http.Handler, error) {
	m, e := CurrentManager()
	if e != nil {
		return nil, e
	}
	return m.MetricsHandler(), nil
}

// AddProvider 运行期向指定链动态新增提供商（自动故障切换仍生效）。
func AddProvider(ctx context.Context, chainName string, p ProviderConfig) error {
	i, e := On(chainName)
	if e != nil {
		return e
	}
	return i.AddProvider(ctx, p)
}

// RemoveProvider 运行期移除指定链的某提供商并关闭其连接。
func RemoveProvider(chainName, id string) error {
	i, e := On(chainName)
	if e != nil {
		return e
	}
	return i.RemoveProvider(id)
}
func AddChain(name string, cfg ChainConfig) error {
	m, e := CurrentManager()
	if e != nil {
		return e
	}
	return m.AddChain(name, cfg)
}
func RemoveChain(name string) error {
	m, e := CurrentManager()
	if e != nil {
		return e
	}
	return m.RemoveChain(name)
}

// Call 对默认链发起一次原始 JSON-RPC 调用，返回原始 JSON 字节。
// 适用于任意自定义方法（debug_*、trace_* 等），无方法白名单。
func Call(ctx context.Context, method string, args ...any) ([]byte, error) {
	i, e := current()
	if e != nil {
		return nil, e
	}
	return i.CallRaw(ctx, method, args...)
}

// CallInto 对默认链发起 RPC 调用并把结果反序列化到 result。
func CallInto(ctx context.Context, result any, method string, args ...any) error {
	i, e := current()
	if e != nil {
		return e
	}
	return i.Call(ctx, result, method, args...)
}

// CallOn 对指定链发起 RPC 调用并把结果反序列化到 result。
func CallOn(ctx context.Context, chainName string, result any, method string, args ...any) error {
	i, e := On(chainName)
	if e != nil {
		return e
	}
	return i.Call(ctx, result, method, args...)
}

// BatchOn 对指定链发起一次 JSON-RPC 批处理。
func BatchOn(ctx context.Context, chainName string, elems []BatchElem) error {
	i, err := On(chainName)
	if err != nil {
		return err
	}
	return i.Batch(ctx, elems)
}

// BlockNumberOn 返回指定链最新区块号。
func BlockNumberOn(ctx context.Context, chainName string) (uint64, error) {
	i, err := On(chainName)
	if err != nil {
		return 0, err
	}
	return i.BlockNumber(ctx)
}

// EthCallOn 对指定链执行 eth_call。
func EthCallOn(ctx context.Context, chainName string, msg ethereum.CallMsg, block string) ([]byte, error) {
	i, err := On(chainName)
	if err != nil {
		return nil, err
	}
	return i.EthCall(ctx, msg, block)
}

// GetLogsOn 对指定链执行 eth_getLogs。
func GetLogsOn(ctx context.Context, chainName string, q ethereum.FilterQuery) ([]types.Log, error) {
	i, err := On(chainName)
	if err != nil {
		return nil, err
	}
	return i.GetLogs(ctx, q)
}

// BalanceAtOn 返回指定链某地址在指定区块的余额。
func BalanceAtOn(ctx context.Context, chainName string, a common.Address, block string) (*big.Int, error) {
	i, err := On(chainName)
	if err != nil {
		return nil, err
	}
	return i.BalanceAt(ctx, a, block)
}

// SubscribeOn 对指定链建立逻辑 WS 订阅（自动重连与重新订阅）。
func SubscribeOn(ctx context.Context, chainName, namespace string, channel any, args ...any) error {
	i, err := On(chainName)
	if err != nil {
		return err
	}
	return i.Subscribe(ctx, namespace, channel, args...)
}

// Batch 对默认链发起一次 JSON-RPC 批处理。
func Batch(ctx context.Context, elems []BatchElem) error {
	i, e := current()
	if e != nil {
		return e
	}
	return i.Batch(ctx, elems)
}

// BlockNumber 返回默认链最新区块号。
func BlockNumber(ctx context.Context) (uint64, error) {
	i, e := current()
	if e != nil {
		return 0, e
	}
	return i.BlockNumber(ctx)
}

// EthCall 对默认链执行 eth_call。
func EthCall(ctx context.Context, msg ethereum.CallMsg, block string) ([]byte, error) {
	i, e := current()
	if e != nil {
		return nil, e
	}
	return i.EthCall(ctx, msg, block)
}

// GetLogs 对默认链执行 eth_getLogs。
func GetLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	i, e := current()
	if e != nil {
		return nil, e
	}
	return i.GetLogs(ctx, q)
}

// BalanceAt 返回默认链某地址在指定区块的余额。
func BalanceAt(ctx context.Context, a common.Address, block string) (*big.Int, error) {
	i, e := current()
	if e != nil {
		return nil, e
	}
	return i.BalanceAt(ctx, a, block)
}

// Subscribe 对默认链建立逻辑 WS 订阅（自动重连与重新订阅）。
func Subscribe(ctx context.Context, namespace string, channel any, args ...any) error {
	i, e := current()
	if e != nil {
		return e
	}
	return i.Subscribe(ctx, namespace, channel, args...)
}

// SubscribeLogs 对默认链建立可靠的日志订阅（支持回填、去重与重组回退）。
func SubscribeLogs(ctx context.Context, q ethereum.FilterQuery) (<-chan types.Log, error) {
	i, e := current()
	if e != nil {
		return nil, e
	}
	return i.SubscribeLogs(ctx, q)
}

// SubscribeLogsOn 对指定链建立可靠的日志订阅。
func SubscribeLogsOn(ctx context.Context, chainName string, q ethereum.FilterQuery) (<-chan types.Log, error) {
	i, e := On(chainName)
	if e != nil {
		return nil, e
	}
	return i.SubscribeLogs(ctx, q)
}

// ProviderStatus 返回指定链全部提供商的健康快照。
func ProviderStatus(chainName string) ([]provider.Snapshot, error) {
	i, e := On(chainName)
	if e != nil {
		return nil, e
	}
	return i.Providers(), nil
}

// SetHTTPSelector 为指定链安装自定义 HTTP 提供商选择策略。
func SetHTTPSelector(chainName string, selector provider.Selector) error {
	i, err := On(chainName)
	if err != nil {
		return err
	}
	i.SetHTTPSelector(selector)
	return nil
}

// SetWSSelector 为指定链安装自定义 WebSocket 提供商选择策略。
func SetWSSelector(chainName string, selector provider.Selector) error {
	i, err := On(chainName)
	if err != nil {
		return err
	}
	i.SetWSSelector(selector)
	return nil
}
