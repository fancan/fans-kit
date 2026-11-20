package manager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fancan/fans-kit/mysql/config"
	mysqldriver "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

// Manager mysql管理者抽象
type Manager struct {
	db           *gorm.DB         // 带读写分离的gorm.DB
	master       *sql.DB          // 主库节点
	masterName   string           // 主库名称
	replicas     map[string]*node // 从库节点
	replicaOrder []string         // 从库名称
	allNodes     []*node          // 所有节点（含主库，供健康检测/统计使用）

	mu               sync.RWMutex
	closed           bool               // 管理者是否已经关闭
	poolCfg          config.PoolConfig  // 连接池配置
	failureThreshold int                // 故障阈值
	healthCancel     context.CancelFunc // 健康检测cancel函数
	healthWG         sync.WaitGroup     // 健康检测组锁
	closeOnce        sync.Once          // 关闭一次
	closeErr         error              // 关闭时的错误
}

// New 创建一个管理者
// 流程：
// 1、根据配置创建主库数据库连接池
// 2、根据配置创建所有从库数据库连接池
// 3、打开gorm
// 4、注入从库到gorm
// 5、注册callback
// 6、开启健康检测
func New(ctx context.Context, cfg config.Config) (*Manager, error) {
	cfg.SetDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m := &Manager{replicas: make(map[string]*node), poolCfg: cfg.Pool, failureThreshold: cfg.Health.FailureThreshold}
	fail := func(err error) (*Manager, error) { _ = m.Close(); return nil, err }

	master, err := openPool(ctx, cfg.Master, cfg.Pool)
	if err != nil {
		return nil, fmt.Errorf("mysql: open master: %w", err)
	}
	m.master = master
	m.masterName = cfg.Master.Name
	// 主库也纳入节点集合，使健康检测与 Stats 覆盖它（原先只有从库被监控）。
	m.allNodes = append(m.allNodes, newNode(cfg.Master.Name, master))

	replicaDialectors := make([]gorm.Dialector, 0, len(cfg.Replicas))
	for _, nc := range cfg.Replicas {
		pool, e := openPool(ctx, nc, cfg.Pool)
		if e != nil {
			return fail(fmt.Errorf("mysql: open replica %s: %w", nc.Name, e))
		}
		n := newNode(nc.Name, pool)
		m.replicas[nc.Name] = n
		m.replicaOrder = append(m.replicaOrder, nc.Name)
		m.allNodes = append(m.allNodes, n)
		replicaDialectors = append(replicaDialectors, gormmysql.New(gormmysql.Config{Conn: pool, SkipInitializeWithVersion: true}))
	}

	// 打开GORM
	gormCfg := &gorm.Config{}
	if cfg.GORMConfig != nil {
		cloned := *cfg.GORMConfig
		gormCfg = &cloned
	}
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: master, SkipInitializeWithVersion: true}), gormCfg)
	if err != nil {
		return fail(fmt.Errorf("mysql: initialize gorm: %w", err))
	}
	m.db = db

	// 读写分离插件应用
	resolver := dbresolver.Register(dbresolver.Config{
		Replicas:          replicaDialectors,
		Policy:            newHealthPolicy(master, m.allNodes),
		TraceResolverMode: cfg.TraceResolverMode,
	})
	if err := db.Use(resolver); err != nil {
		return fail(fmt.Errorf("mysql: register dbresolver: %w", err))
	}

	if err := registerCallbacks(db, m.allNodes, cfg.Health.FailureThreshold, cfg.Health.Enabled); err != nil {
		return fail(fmt.Errorf("mysql: register callbacks: %w", err))
	}

	if cfg.Health.Enabled && len(m.allNodes) > 0 {
		m.startHealthChecker(cfg.Health)
	}
	return m, nil
}

// openPool 打开一个数据库连接池
func openPool(ctx context.Context, nc config.NodeConfig, pc config.PoolConfig) (*sql.DB, error) {
	if _, err := mysqldriver.ParseDSN(nc.DSN); err != nil {
		return nil, fmt.Errorf("invalid DSN: %w", err)
	}
	db, err := sql.Open("mysql", nc.DSN)
	if err != nil {
		return nil, err
	}
	applyPoolConfig(db, pc)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// applyPoolConfig 应用连接池配置数据
func applyPoolConfig(db *sql.DB, c config.PoolConfig) {
	db.SetMaxIdleConns(c.MaxIdleConns)
	db.SetMaxOpenConns(c.MaxOpenConns)
	db.SetConnMaxLifetime(c.ConnMaxLifetime)
	db.SetConnMaxIdleTime(c.ConnMaxIdleTime)
}

// DB 获取读写分离的gorm.DB
func (m *Manager) DB() *gorm.DB { return m.db }

// MasterDB 强制获取主库DB
func (m *Manager) MasterDB() *gorm.DB { return m.db.Clauses(dbresolver.Write) }

// ReadDB 强制获取从库DB
func (m *Manager) ReadDB() *gorm.DB { return m.db.Clauses(dbresolver.Read) }

// MasterSQLDB 获取主库的sql.DB
func (m *Manager) MasterSQLDB() *sql.DB { return m.master }

// ReplicaSQLDB 通过名称获取从库的sql.DB
func (m *Manager) ReplicaSQLDB(name string) (*sql.DB, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n, ok := m.replicas[name]
	if !ok {
		return nil, false
	}
	return n.db, true
}

// ReplicaNames 获取所有的从库名称
func (m *Manager) ReplicaNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.replicaOrder...)
}

// Close 关闭管理者
func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		cancel := m.healthCancel
		m.mu.Unlock()
		if cancel != nil {
			cancel()
			m.healthWG.Wait()
		}
		var errs []error
		if m.master != nil {
			if err := m.master.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close master: %w", err))
			}
		}
		for _, name := range m.replicaOrder {
			if n := m.replicas[name]; n != nil {
				if err := n.db.Close(); err != nil {
					errs = append(errs, fmt.Errorf("close %s: %w", name, err))
				}
			}
		}
		m.closeErr = errors.Join(errs...)
	})
	return m.closeErr
}

// UpdatePoolConfig 动态修改database/sql连接池(无需重新连接)
func (m *Manager) UpdatePoolConfig(cfg config.PoolConfig) error {
	if err := config.ValidatePool(cfg); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("mysql: manager is closed")
	}
	applyPoolConfig(m.master, cfg)
	for _, n := range m.replicas {
		applyPoolConfig(n.db, cfg)
	}
	m.poolCfg = cfg
	return nil
}

// PoolConfig 获取当前的PoolConfig
func (m *Manager) PoolConfig() config.PoolConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.poolCfg
}

// Health 获取当前所有节点的健康快照
func (m *Manager) Health() []NodeHealth {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]NodeHealth, 0, len(m.allNodes))
	for _, n := range m.allNodes {
		out = append(out, n.snapshot())
	}
	return out
}

// startHealthChecker 开启健康检测
func (m *Manager) startHealthChecker(cfg config.HealthConfig) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.healthCancel = cancel
	m.mu.Unlock()
	m.healthWG.Go(func() {
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.probe(ctx, cfg)
			}
		}
	})
}

// probe 健康探针
func (m *Manager) probe(parent context.Context, cfg config.HealthConfig) {
	m.mu.RLock()
	nodes := append([]*node(nil), m.allNodes...)
	closed := m.closed
	m.mu.RUnlock()
	if closed || len(nodes) == 0 {
		return
	}

	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Add(1)
		go func(n *node) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(parent, cfg.ProbeTimeout)
			err := n.db.PingContext(ctx)
			cancel()
			if err != nil {
				n.markFailure(cfg.FailureThreshold, err)
				return
			}
			n.markSuccess(cfg.SuccessThreshold)
		}(n)
	}
	wg.Wait()
}
