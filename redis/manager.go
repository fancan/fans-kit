package redis

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const defaultName = "default"

// manager 是包内部的实例管理者。
//
// 之所以不导出：本模块的定位是「全局零侵入」——业务只调用 redis.Get / redis.Named
// 等包级函数，不需要、也不应该自行持有或构造管理者。把所有实例的注册、查找、
// 关闭收敛到包内单例，可以保证生命周期只有一条路径，避免外部重复 Close / 提前
// Close 导致的状态不一致。
type manager struct {
	mu          sync.RWMutex
	instances   map[string]*Instance
	defaultName string
	generation  uint64
}

// newManager 创建一个内部管理者。
func newManager() *manager {
	return &manager{instances: make(map[string]*Instance), defaultName: defaultName}
}

// initDefault 初始化名为 default 的实例，并将其设为默认实例。
func (m *manager) initDefault(ctx context.Context, cfg Config) error {
	return m.initNamed(ctx, defaultName, cfg, true)
}

// initNamed 初始化指定名称的实例。
// makeDefault 为 true 时强制设为默认实例；为 false 时，仅当当前没有可用的默认
// 实例才自动补位（见下方 defaultName 维护逻辑）。
func (m *manager) initNamed(ctx context.Context, name string, cfg Config, makeDefault bool) error {
	if name == "" {
		return fmt.Errorf("%w: empty instance name", ErrInvalidConfig)
	}
	cfg, err := cfg.normalized()
	if err != nil {
		return err
	}

	m.mu.RLock()
	_, exists := m.instances[name]
	generation := m.generation
	m.mu.RUnlock()
	if exists {
		return fmt.Errorf("%w: %s", ErrAlreadyExists, name)
	}

	// 建连 + ping 是慢操作，放在锁外，避免阻塞其它实例的读取。
	client, metrics, err := newClient(cfg)
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = ping(pingCtx, client)
	cancel()
	if err != nil {
		_ = client.Close()
		return err
	}

	inst := &Instance{name: name, client: client, prefix: cfg.Prefix, metrics: metrics}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.instances == nil {
		m.instances = make(map[string]*Instance)
	}
	// 双重检查：建连期间可能有并发注册同名实例。
	if _, exists = m.instances[name]; exists {
		_ = client.Close()
		return fmt.Errorf("%w: %s", ErrAlreadyExists, name)
	}
	// 建连期间执行过 Close：本次初始化作废，避免把客户端挂到已重置的管理者上。
	if generation != m.generation {
		_ = client.Close()
		return ErrManagerClosedDuringInit
	}
	m.instances[name] = inst
	m.resolveDefaultName(name, makeDefault)
	return nil
}

// resolveDefaultName 决定在成功注册 name 实例后，默认实例应指向谁。
//
// 规则：
//  1. makeDefault 为 true 时强制指向 name；
//  2. 否则仅当当前默认实例不存在（尚未注册或已被移除）时补位到 name；
//  3. 否则保持原默认实例不变。
//
// 规则 2 修复了一个可用性缺陷：此前只有一个 InitNamed("cache") 实例时，
// defaultName 仍是 "default"，Default() 永远返回 ErrNotInitialized，
// 导致 redis.Get/Set 等所有包级函数不可用。
//
// 调用方必须持有 m.mu 写锁。
func (m *manager) resolveDefaultName(name string, makeDefault bool) {
	if makeDefault || m.instances[m.defaultName] == nil {
		m.defaultName = name
	}
}

// defaultInstance 返回默认实例。
func (m *manager) defaultInstance() (*Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst := m.instances[m.defaultName]
	if inst == nil {
		return nil, ErrNotInitialized
	}
	return inst, nil
}

// named 返回指定名称的实例。
func (m *manager) named(name string) (*Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst := m.instances[name]
	if inst == nil {
		return nil, fmt.Errorf("%w: %s", ErrInstanceNotFound, name)
	}
	return inst, nil
}

// list 返回全部实例（按名称排序），供指标导出使用。
func (m *manager) list() []*Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Instance, 0, len(m.instances))
	for _, inst := range m.instances {
		out = append(out, inst)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// close 关闭并移除全部实例。
func (m *manager) close() error {
	m.mu.Lock()
	instances := m.instances
	m.generation++
	m.instances = make(map[string]*Instance)
	m.defaultName = defaultName
	m.mu.Unlock()

	var firstErr error
	for _, inst := range instances {
		if err := inst.client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// global 是包级单例，所有包级函数都以它为入口。
var global = newManager()

// Init 初始化默认实例。
func Init(cfg Config) error { return global.initDefault(context.Background(), cfg) }

// InitContext 初始化默认实例，使用调用方 context 控制建连与首包 ping 的超时。
func InitContext(ctx context.Context, cfg Config) error { return global.initDefault(ctx, cfg) }

// InitNamed 注册一个具名实例（不抢占已有默认实例）。
func InitNamed(name string, cfg Config) error {
	return global.initNamed(context.Background(), name, cfg, false)
}

// InitNamedContext 注册一个具名实例，使用调用方 context。
func InitNamedContext(ctx context.Context, name string, cfg Config) error {
	return global.initNamed(ctx, name, cfg, false)
}

// InitNamedAsDefault 注册一个具名实例并强制设为默认实例。
func InitNamedAsDefault(name string, cfg Config) error {
	return global.initNamed(context.Background(), name, cfg, true)
}

// Default 返回默认实例。
func Default() (*Instance, error) { return global.defaultInstance() }

// Named 返回指定名称的实例。
func Named(name string) (*Instance, error) { return global.named(name) }

// Close 关闭全部实例并释放连接池。
func Close() error { return global.close() }

// Client 返回默认实例底层的 go-redis 通用客户端（高级逃生口，通常无需使用）。
func Client() (goredis.UniversalClient, error) {
	inst, err := Default()
	if err != nil {
		return nil, err
	}
	return inst.Client(), nil
}
