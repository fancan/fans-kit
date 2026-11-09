package chain

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fancan/fans-kit/chain/config"
	"github.com/fancan/fans-kit/chain/metrics"
	"github.com/fancan/fans-kit/chain/provider"
)

// Instance 表示一条链的运行时实例：持有独立的HTTP/WS提供商池、
// 健康检查/区块监控后台循环以及订阅的生命周期管理。
type Instance struct {
	name             string
	cfg              config.ChainConfig
	httpPool, wsPool *provider.Pool
	metrics          *metrics.Registry
	stop             chan struct{}
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
	subWG            sync.WaitGroup
	lifeMu           sync.Mutex
	closed           bool
	closeOnce        sync.Once
}

// NewInstance 依据链配置创建实例：对每个提供商的 HTTP/WS 地址分别拨号并加入
// 对应池（HTTP为必需，WS可选），随后按需启动健康检查/区块监控协程。
func NewInstance(name string, cfg config.ChainConfig, m *metrics.Registry) (*Instance, error) {
	cfg = cfg.Normalized()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("chain %s: %w", name, err)
	}
	if m == nil {
		m = metrics.New()
	}
	instanceCtx, instanceCancel := context.WithCancel(context.Background())
	i := &Instance{name: name, cfg: cfg, metrics: m, stop: make(chan struct{}), ctx: instanceCtx, cancel: instanceCancel, httpPool: provider.NewPool(cfg.Health.FailureThreshold, cfg.Health.SuccessThreshold, cfg.Health.Cooldown.Value()), wsPool: provider.NewPool(cfg.Health.FailureThreshold, cfg.Health.SuccessThreshold, cfg.Health.Cooldown.Value())}
	for _, pc := range cfg.Providers {
		if pc.HTTPURL != "" {
			hc := pc
			hc.WSURL = ""
			dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			e, err := provider.NewEndpoint(dialCtx, hc)
			cancel()
			if err != nil {
				i.Close()
				return nil, err
			}
			if err = i.httpPool.Add(e); err != nil {
				e.Close()
				i.Close()
				return nil, err
			}
		}
		if pc.WSURL != "" {
			wc := pc
			wc.HTTPURL = ""
			dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			e, err := provider.NewEndpoint(dialCtx, wc)
			cancel()
			if err != nil {
				i.Close()
				return nil, err
			}
			if err = i.wsPool.Add(e); err != nil {
				e.Close()
				i.Close()
				return nil, err
			}
		}
	}
	if len(i.httpPool.All()) == 0 {
		i.Close()
		return nil, fmt.Errorf("chain %s: at least one HTTP endpoint required", name)
	}
	if cfg.Health.Enabled || cfg.Block.Enabled {
		i.wg.Add(1)
		go i.monitorLoop()
	}
	return i, nil
}

// Name 返回链名称。
func (i *Instance) Name() string { return i.name }

// ChainID 返回链 ID。
func (i *Instance) ChainID() uint64 { return i.cfg.ChainID }

// Close 关闭实例：取消上下文、停止监控循环、等待订阅退出并关闭所有连接。
// 通过 sync.Once 保证幂等。
func (i *Instance) Close() error {
	i.closeOnce.Do(func() {
		i.lifeMu.Lock()
		i.closed = true
		i.cancel()
		close(i.stop)
		i.lifeMu.Unlock()
		i.wg.Wait()
		i.subWG.Wait()
		for _, e := range i.httpPool.All() {
			e.Close()
		}
		for _, e := range i.wsPool.All() {
			e.Close()
		}
	})
	return nil
}

// bindContext 把调用方传入的 context 与实例生命周期绑定：
// 实例关闭时，派生出的 ctx 会被自动取消，从而让长驻操作（如订阅）随之退出。
// 返回的 cleanup 必须在长驻操作退出时调用，以释放 context.AfterFunc 注册的回调。
func (i *Instance) bindContext(parent context.Context) (context.Context, func()) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(i.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}

// startSubscription 在实例未关闭时登记并启动一个订阅协程，登记/关闭检查在
// lifeMu 保护下进行，避免与 Close 竞态。
func (i *Instance) startSubscription(parent context.Context, run func(context.Context)) error {
	i.lifeMu.Lock()
	if i.closed {
		i.lifeMu.Unlock()
		return errors.New("chain instance is closed")
	}
	i.subWG.Add(1)
	i.lifeMu.Unlock()
	ctx, cleanup := i.bindContext(parent)
	go func() { defer i.subWG.Done(); defer cleanup(); run(ctx) }()
	return nil
}

// recordHTTP 记录一 HTTP调用的结果并刷新该提供商的监控指标。
func (i *Instance) recordHTTP(ep *provider.Endpoint, err error, latency time.Duration) {
	i.httpPool.Record(ep, err, latency)
	s := ep.Snapshot()
	i.metrics.SetProvider(i.name, s.ID, s.Transport, s.Status.String(), s.LatestBlock, uint64(s.Latency), s.InFlight, s.TotalFailures, s.ConsistencyFailures)
}

// recordWS 记录一次WebSocket调用的结果并刷新该提供商的监控指标。
func (i *Instance) recordWS(ep *provider.Endpoint, err error, latency time.Duration) {
	i.wsPool.Record(ep, err, latency)
	s := ep.Snapshot()
	i.metrics.SetProvider(i.name, s.ID, s.Transport, s.Status.String(), s.LatestBlock, uint64(s.Latency), s.InFlight, s.TotalFailures, s.ConsistencyFailures)
}
