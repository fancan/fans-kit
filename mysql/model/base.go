package model

import (
	"context"
	mysqlkit "github.com/fancan/fans-kit/mysql"
	"gorm.io/gorm"
)

// BaseModel 沿用GORM's APIs.
type BaseModel struct{}

func (BaseModel) DB() *gorm.DB                               { return mysqlkit.DB() }
func (BaseModel) Master() *gorm.DB                           { return mysqlkit.Master() }
func (BaseModel) Read() *gorm.DB                             { return mysqlkit.Read() }
func (BaseModel) DBWithContext(ctx context.Context) *gorm.DB { return mysqlkit.DB().WithContext(ctx) }
func (BaseModel) StrongDB(ctx context.Context) *gorm.DB {
	return mysqlkit.DB().WithContext(mysqlkit.WithStrongConsistency(ctx))
}
func (BaseModel) Transaction(fn func(*gorm.DB) error) error { return mysqlkit.Transaction(fn) }
func (BaseModel) TransactionContext(ctx context.Context, fn func(*gorm.DB) error) error {
	return mysqlkit.TransactionContext(ctx, fn)
}
