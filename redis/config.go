package redis

import (
	"crypto/tls"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// redis的部署模式
type Mode string

const (
	ModeStandalone Mode = "standalone" // redis单机模式
	ModeFailover   Mode = "failover"   // redis哨兵模式
	ModeCluster    Mode = "cluster"    // redis集群模式
)

type Config struct {
	Mode     Mode
	Addrs    []string
	Username string
	Password string
	DB       int

	// 哨兵模式下的一些验证
	MasterName       string
	SentinelUsername string
	SentinelPassword string

	// 连接池配置
	PoolSize       int
	MinIdleConns   int
	MaxIdleConns   int
	MaxActiveConns int

	// 超时配置
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	PoolTimeout  time.Duration

	ConnMaxIdleTime time.Duration
	ConnMaxLifetime time.Duration

	// 重试配置
	MaxRetries      int
	MinRetryBackoff time.Duration
	MaxRetryBackoff time.Duration

	ClientName string
	TLSConfig  *tls.Config

	Prefix string // 应用前缀

	// 仅集群模式下的一些路由选项配置
	ReadOnly       bool
	RouteByLatency bool
	RouteRandomly  bool

	DisableMetrics bool           // 是否开启监测 通过go-redis的hook机制注册进去
	Hooks          []goredis.Hook // hook
}

// normalized 配置归一化
func (c Config) normalized() (Config, error) {
	if c.Mode == "" {
		c.Mode = ModeStandalone
	}
	c.Addrs = append([]string(nil), c.Addrs...)
	c.Hooks = append([]goredis.Hook(nil), c.Hooks...)
	if c.TLSConfig != nil {
		c.TLSConfig = c.TLSConfig.Clone()
	}
	if len(c.Addrs) == 0 {
		return c, fmt.Errorf("%w: at least one address is required", ErrInvalidConfig)
	}
	for _, addr := range c.Addrs {
		if addr == "" {
			return c, fmt.Errorf("%w: address cannot be empty", ErrInvalidConfig)
		}
	}
	if c.DB < 0 {
		return c, fmt.Errorf("%w: DB cannot be negative", ErrInvalidConfig)
	}
	switch c.Mode {
	case ModeStandalone:
		if len(c.Addrs) != 1 {
			return c, fmt.Errorf("%w: standalone mode requires exactly one address", ErrInvalidConfig)
		}
	case ModeFailover:
		if c.MasterName == "" {
			return c, fmt.Errorf("%w: sentinel mode requires MasterName", ErrInvalidConfig)
		}
	case ModeCluster:
		if c.DB != 0 {
			return c, fmt.Errorf("%w: Redis Cluster only supports DB 0", ErrInvalidConfig)
		}
	default:
		return c, fmt.Errorf("%w: unsupported mode %q", ErrInvalidConfig, c.Mode)
	}

	if c.PoolSize == 0 {
		c.PoolSize = 10
	}
	if c.MinIdleConns == 0 {
		c.MinIdleConns = 2
	}
	if c.DialTimeout == 0 {
		c.DialTimeout = 5 * time.Second
	}
	if c.ReadTimeout == 0 {
		c.ReadTimeout = 3 * time.Second
	}
	if c.WriteTimeout == 0 {
		c.WriteTimeout = 3 * time.Second
	}
	if c.PoolTimeout == 0 {
		c.PoolTimeout = 4 * time.Second
	}
	if c.ConnMaxIdleTime == 0 {
		c.ConnMaxIdleTime = 5 * time.Minute
	}
	if c.ConnMaxLifetime == 0 {
		c.ConnMaxLifetime = 30 * time.Minute
	}
	if c.MaxRetries == 0 {
		c.MaxRetries = 3
	}
	if c.MinRetryBackoff == 0 {
		c.MinRetryBackoff = 8 * time.Millisecond
	}
	if c.MaxRetryBackoff == 0 {
		c.MaxRetryBackoff = 512 * time.Millisecond
	}

	if c.PoolSize < 1 || c.MinIdleConns < 0 || c.MaxIdleConns < 0 || c.MaxActiveConns < 0 {
		return c, fmt.Errorf("%w: invalid connection pool settings", ErrInvalidConfig)
	}
	if c.MaxIdleConns > 0 && c.MinIdleConns > c.MaxIdleConns {
		return c, fmt.Errorf("%w: MinIdleConns cannot exceed MaxIdleConns", ErrInvalidConfig)
	}
	if c.MaxRetries < -1 {
		return c, fmt.Errorf("%w: MaxRetries must be -1 or greater", ErrInvalidConfig)
	}
	return c, nil
}
