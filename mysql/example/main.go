package main

import (
	"context"
	"log"
	"time"

	mysqlkit "github.com/fancan/fans-kit/mysql"
	"github.com/fancan/fans-kit/mysql/model"
	"gorm.io/gorm"
)

func main() {
	// 入口处初始化好
	err := mysqlkit.InitContext(context.Background(), mysqlkit.Config{
		Master: mysqlkit.NodeConfig{DSN: "user:password@tcp(127.0.0.1:3306)/dex?charset=utf8mb4&parseTime=True&loc=Local"},
		Replicas: []mysqlkit.NodeConfig{
			{Name: "read-1", DSN: "user:password@tcp(127.0.0.1:3307)/dex?charset=utf8mb4&parseTime=True&loc=Local"},
			{Name: "read-2", DSN: "user:password@tcp(127.0.0.1:3308)/dex?charset=utf8mb4&parseTime=True&loc=Local"},
		},
		Pool:   mysqlkit.PoolConfig{MaxIdleConns: 10, MaxOpenConns: 100, ConnMaxLifetime: time.Hour, ConnMaxIdleTime: 10 * time.Minute},
		Health: mysqlkit.HealthConfig{Enabled: true, Interval: 5 * time.Second, ProbeTimeout: 2 * time.Second, FailureThreshold: 2, SuccessThreshold: 2},
	})
	if err != nil {
		log.Fatal(err)
	}
	// defer 关闭
	defer func() {
		if err := mysqlkit.Close(); err != nil {
			log.Printf("close mysql: %v", err)
		}
	}()

	// DB() 写操作会自动路由到主库，读操作会自动路由到健康可用的从库
	var pools []model.PoolRecord
	if err := mysqlkit.DB().Find(&pools).Error; err != nil {
		log.Printf("find pools: %v", err)
	}

	// 请求作用域内强制使用主库 适用于写后立即读避免主从延迟导致的一致性问题
	ctx := mysqlkit.WithStrongConsistency(context.Background())
	var pool model.PoolRecord
	if err := mysqlkit.DB().WithContext(ctx).First(&pool, 1).Error; err != nil {
		log.Printf("strong read: %v", err)
	}

	// 事务在主库操作 Transaction starts on master. Always use tx inside callback.
	err = mysqlkit.Transaction(func(tx *gorm.DB) error {
		return tx.Create(&model.PoolRecord{PoolAddress: "0xabc", ProtocolType: 1}).Error
	})
	if err != nil {
		log.Printf("transaction: %v", err)
	}

	// Runtime pool adjustment; applied to master and every replica.
	if err := mysqlkit.UpdatePoolConfig(mysqlkit.PoolConfig{MaxIdleConns: 20, MaxOpenConns: 200, ConnMaxLifetime: 2 * time.Hour, ConnMaxIdleTime: 15 * time.Minute}); err != nil {
		log.Printf("update pool: %v", err)
	}

	stats, _ := mysqlkit.Stats()
	health, _ := mysqlkit.Health()
	log.Printf("stats=%+v health=%+v", stats, health)
}
