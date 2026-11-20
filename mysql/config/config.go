package config

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// NodeConfig 管理者所管理的节点抽象
type NodeConfig struct{ Name, DSN string }

// PoolConfig 连接池配置
type PoolConfig struct {
	MaxIdleConns    int           // 最大空闲连接数
	MaxOpenConns    int           // 最大打开连接数
	ConnMaxLifetime time.Duration // 连接最大生命周期
	ConnMaxIdleTime time.Duration // 连接最大空闲时间
}

// HealthConfig 健康检测相关配置
type HealthConfig struct {
	Enabled          bool          // 是否开启健康检测
	Interval         time.Duration // 检测间隔时间
	ProbeTimeout     time.Duration // 探针超时时间
	FailureThreshold int           // 故障阈值
	SuccessThreshold int           // 成功阈值
}

// Config mysql公共模块配置
type Config struct {
	Master            NodeConfig   // 主库节点配置
	Replicas          []NodeConfig // 从库节点配置
	Pool              PoolConfig   // 连接池配置
	Health            HealthConfig // 健康检测相关配置
	GORMConfig        *gorm.Config // gorm配置
	TraceResolverMode bool         // 是否跟踪
}

// SetDefaults 设置默认值
func (c *Config) SetDefaults() {
	if c.Master.Name == "" {
		c.Master.Name = "master"
	}
	if c.Pool.MaxIdleConns == 0 {
		c.Pool.MaxIdleConns = 10
	}
	if c.Pool.MaxOpenConns == 0 {
		c.Pool.MaxOpenConns = 100
	}
	if c.Pool.ConnMaxLifetime == 0 {
		c.Pool.ConnMaxLifetime = time.Hour
	}
	if c.Pool.ConnMaxIdleTime == 0 {
		c.Pool.ConnMaxIdleTime = 10 * time.Minute
	}
	if c.Health.Interval == 0 {
		c.Health.Interval = 5 * time.Second
	}
	if c.Health.ProbeTimeout == 0 {
		c.Health.ProbeTimeout = 2 * time.Second
	}
	if c.Health.FailureThreshold == 0 {
		c.Health.FailureThreshold = 2
	}
	if c.Health.SuccessThreshold == 0 {
		c.Health.SuccessThreshold = 2
	}
	for i := range c.Replicas {
		if c.Replicas[i].Name == "" {
			c.Replicas[i].Name = fmt.Sprintf("replica-%d", i+1)
		}
	}
}

// ValidatePool 检测PoolConfig配置是否达标
func ValidatePool(c PoolConfig) error {
	if c.MaxIdleConns < 0 || c.MaxOpenConns < 0 {
		return errors.New("mysql: pool limits cannot be negative")
	}
	if c.MaxOpenConns > 0 && c.MaxIdleConns > c.MaxOpenConns {
		return errors.New("mysql: MaxIdleConns cannot exceed MaxOpenConns")
	}
	if c.ConnMaxLifetime < 0 || c.ConnMaxIdleTime < 0 {
		return errors.New("mysql: connection durations cannot be negative")
	}
	return nil
}

// Validate 检测Config是否达标
func (c Config) Validate() error {
	if c.Master.DSN == "" {
		return errors.New("mysql: master DSN is required")
	}
	if c.Master.Name == "" {
		return errors.New("mysql: master name is required")
	}
	if err := ValidatePool(c.Pool); err != nil {
		return err
	}
	if c.Health.Interval <= 0 || c.Health.ProbeTimeout <= 0 {
		return errors.New("mysql: health interval and probe timeout must be positive")
	}
	if c.Health.FailureThreshold < 1 || c.Health.SuccessThreshold < 1 {
		return errors.New("mysql: health thresholds must be >= 1")
	}
	names := map[string]struct{}{c.Master.Name: {}}
	for i, n := range c.Replicas {
		if n.DSN == "" {
			return fmt.Errorf("mysql: replica[%d] DSN is required", i)
		}
		if n.Name == "" {
			return fmt.Errorf("mysql: replica[%d] name is required", i)
		}
		if _, ok := names[n.Name]; ok {
			return fmt.Errorf("mysql: duplicate node name %q", n.Name)
		}
		names[n.Name] = struct{}{}
	}
	return nil
}
