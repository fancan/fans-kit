package redis

import (
	"context"
	"fmt"

	goredis "github.com/redis/go-redis/v9"
)

// newClient 创建一个 go-redis 客户端。
// 统一通过 UniversalClient 接口暴露，调用方无需关心后端是单机/哨兵/集群。
func newClient(cfg Config) (goredis.UniversalClient, *metrics, error) {
	base := goredis.UniversalOptions{
		Addrs:           cfg.Addrs,
		Username:        cfg.Username,
		Password:        cfg.Password,
		DB:              cfg.DB,
		PoolSize:        cfg.PoolSize,
		MinIdleConns:    cfg.MinIdleConns,
		MaxIdleConns:    cfg.MaxIdleConns,
		MaxActiveConns:  cfg.MaxActiveConns,
		DialTimeout:     cfg.DialTimeout,
		ReadTimeout:     cfg.ReadTimeout,
		WriteTimeout:    cfg.WriteTimeout,
		PoolTimeout:     cfg.PoolTimeout,
		ConnMaxIdleTime: cfg.ConnMaxIdleTime,
		ConnMaxLifetime: cfg.ConnMaxLifetime,
		MaxRetries:      cfg.MaxRetries,
		MinRetryBackoff: cfg.MinRetryBackoff,
		MaxRetryBackoff: cfg.MaxRetryBackoff,
		TLSConfig:       cfg.TLSConfig,
		ClientName:      cfg.ClientName,
	}

	var client goredis.UniversalClient
	switch cfg.Mode {
	case ModeStandalone:
		client = goredis.NewClient(base.Simple())
	case ModeFailover:
		client = goredis.NewFailoverClient(&goredis.FailoverOptions{
			MasterName:       cfg.MasterName,
			SentinelAddrs:    cfg.Addrs,
			Username:         cfg.Username,
			Password:         cfg.Password,
			SentinelUsername: cfg.SentinelUsername,
			SentinelPassword: cfg.SentinelPassword,
			DB:               cfg.DB,
			PoolSize:         cfg.PoolSize,
			MinIdleConns:     cfg.MinIdleConns,
			MaxIdleConns:     cfg.MaxIdleConns,
			MaxActiveConns:   cfg.MaxActiveConns,
			DialTimeout:      cfg.DialTimeout,
			ReadTimeout:      cfg.ReadTimeout,
			WriteTimeout:     cfg.WriteTimeout,
			PoolTimeout:      cfg.PoolTimeout,
			ConnMaxIdleTime:  cfg.ConnMaxIdleTime,
			ConnMaxLifetime:  cfg.ConnMaxLifetime,
			MaxRetries:       cfg.MaxRetries,
			MinRetryBackoff:  cfg.MinRetryBackoff,
			MaxRetryBackoff:  cfg.MaxRetryBackoff,
			TLSConfig:        cfg.TLSConfig,
			ClientName:       cfg.ClientName,
		})
	case ModeCluster:
		client = goredis.NewClusterClient(&goredis.ClusterOptions{
			Addrs:           cfg.Addrs,
			Username:        cfg.Username,
			Password:        cfg.Password,
			PoolSize:        cfg.PoolSize,
			MinIdleConns:    cfg.MinIdleConns,
			MaxIdleConns:    cfg.MaxIdleConns,
			MaxActiveConns:  cfg.MaxActiveConns,
			DialTimeout:     cfg.DialTimeout,
			ReadTimeout:     cfg.ReadTimeout,
			WriteTimeout:    cfg.WriteTimeout,
			PoolTimeout:     cfg.PoolTimeout,
			ConnMaxIdleTime: cfg.ConnMaxIdleTime,
			ConnMaxLifetime: cfg.ConnMaxLifetime,
			MaxRetries:      cfg.MaxRetries,
			MinRetryBackoff: cfg.MinRetryBackoff,
			MaxRetryBackoff: cfg.MaxRetryBackoff,
			TLSConfig:       cfg.TLSConfig,
			ClientName:      cfg.ClientName,
			ReadOnly:        cfg.ReadOnly,
			RouteByLatency:  cfg.RouteByLatency,
			RouteRandomly:   cfg.RouteRandomly,
		})
	default:
		return nil, nil, fmt.Errorf("%w: unsupported mode %q", ErrInvalidConfig, cfg.Mode)
	}
	var mtr *metrics
	if !cfg.DisableMetrics {
		mtr = newMetrics()
		client.AddHook(mtr)
	}
	for _, hook := range cfg.Hooks {
		if hook != nil {
			client.AddHook(hook)
		}
	}
	return client, mtr, nil
}

// ping redis探活
func ping(ctx context.Context, client goredis.UniversalClient) error {
	if err := client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis: ping failed: %w", err)
	}
	return nil
}
