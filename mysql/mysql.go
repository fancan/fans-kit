// Package mysql is a small global MySQL infrastructure wrapper based on GORM,
// the official DBResolver plugin and database/sql.
package mysql

// 基于gorm、database/mysql构建的轻量级封装
// 使用说明：
// 1、外部业务调用Init() 初始化MySQL相关连接池
// 2、操作各种数据库表
// 3、外部业务系统调用Close() 关闭MySQL相关连接池

import (
	"context"
	"database/sql"

	"github.com/fancan/fans-kit/mysql/config"
	"github.com/fancan/fans-kit/mysql/manager"
	"gorm.io/gorm"
)

type Config = config.Config
type NodeConfig = config.NodeConfig
type PoolConfig = config.PoolConfig
type HealthConfig = config.HealthConfig
type NodeStats = manager.NodeStats
type NodeHealth = manager.NodeHealth

// 生命周期相关
func Init(c Config) error                             { return initfor(context.Background(), c) }
func InitContext(ctx context.Context, c Config) error { return initfor(ctx, c) }
func Close() error                                    { return closefor() }

// 数据库操作相关
func DB() *gorm.DB                                 { db, _ := dB(); return db }
func DBE() (*gorm.DB, error)                       { return dB() }
func Master() *gorm.DB                             { db, _ := master(); return db }
func MasterE() (*gorm.DB, error)                   { return master() }
func Read() *gorm.DB                               { db, _ := read(); return db }
func ReadE() (*gorm.DB, error)                     { return read() }
func Transaction(fn func(tx *gorm.DB) error) error { return transaction(context.Background(), fn) }
func TransactionContext(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return transaction(ctx, fn)
}

// 强制gorm使用此上下文进行读操作(适用写后立即读) eg:mysql.DB().WithContext(mysql.WithPrimary(ctx)).First(&row)
func WithPrimary(ctx context.Context) context.Context           { return manager.WithPrimary(ctx) }
func WithStrongConsistency(ctx context.Context) context.Context { return WithPrimary(ctx) }

// sql.DB相关操作 注意会绕过gorm的DBResolver
func SQLDB() (*sql.DB, error)                   { return masterSQLDB() }
func ReplicaSQLDB(name string) (*sql.DB, error) { return replicaSQLDB(name) }

// 连接池的一下指标监控和动态操作
func Stats() ([]NodeStats, error)            { return stats() }
func Health() ([]NodeHealth, error)          { return health() }
func UpdatePoolConfig(c PoolConfig) error    { return updatePoolConfig(c) }
func CurrentPoolConfig() (PoolConfig, error) { return poolConfig() }
