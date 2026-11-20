package mysql

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/fancan/fans-kit/mysql/config"
	"github.com/fancan/fans-kit/mysql/manager"
	"gorm.io/gorm"
)

var (
	ErrNotInitialized = errors.New("mysql: not initialized")
	ErrAlreadyInit    = errors.New("mysql: already initialized")
)

// 管理者全局变量
var global struct {
	sync.RWMutex
	m *manager.Manager
}

func initfor(ctx context.Context, cfg config.Config) error {
	global.Lock()
	defer global.Unlock()
	if global.m != nil {
		return ErrAlreadyInit
	}
	m, err := manager.New(ctx, cfg)
	if err != nil {
		return err
	}
	global.m = m
	return nil
}
func closefor() error {
	global.Lock()
	m := global.m
	global.m = nil
	global.Unlock()
	if m == nil {
		return nil
	}
	return m.Close()
}
func getManager() (*manager.Manager, error) {
	global.RLock()
	defer global.RUnlock()
	if global.m == nil {
		return nil, ErrNotInitialized
	}
	return global.m, nil
}
func dB() (*gorm.DB, error) {
	m, e := getManager()
	if e != nil {
		return nil, e
	}
	return m.DB(), nil
}
func master() (*gorm.DB, error) {
	m, e := getManager()
	if e != nil {
		return nil, e
	}
	return m.MasterDB(), nil
}
func read() (*gorm.DB, error) {
	m, e := getManager()
	if e != nil {
		return nil, e
	}
	return m.ReadDB(), nil
}
func transaction(ctx context.Context, fn func(*gorm.DB) error) error {
	if fn == nil {
		return errors.New("mysql: nil transaction callback")
	}
	m, e := getManager()
	if e != nil {
		return e
	}
	db := m.MasterDB()
	if ctx != nil {
		db = db.WithContext(ctx)
	}
	return db.Transaction(fn)
}
func masterSQLDB() (*sql.DB, error) {
	m, e := getManager()
	if e != nil {
		return nil, e
	}
	return m.MasterSQLDB(), nil
}
func replicaSQLDB(name string) (*sql.DB, error) {
	m, e := getManager()
	if e != nil {
		return nil, e
	}
	db, ok := m.ReplicaSQLDB(name)
	if !ok {
		return nil, errors.New("mysql: replica not found: " + name)
	}
	return db, nil
}
func stats() ([]manager.NodeStats, error) {
	m, e := getManager()
	if e != nil {
		return nil, e
	}
	return m.Stats(), nil
}
func health() ([]manager.NodeHealth, error) {
	m, e := getManager()
	if e != nil {
		return nil, e
	}
	return m.Health(), nil
}
func updatePoolConfig(c config.PoolConfig) error {
	m, e := getManager()
	if e != nil {
		return e
	}
	return m.UpdatePoolConfig(c)
}
func poolConfig() (config.PoolConfig, error) {
	m, e := getManager()
	if e != nil {
		return config.PoolConfig{}, e
	}
	return m.PoolConfig(), nil
}
