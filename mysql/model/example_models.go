package model

import (
	"errors"
	"time"
)

type PoolRecord struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ChainId      uint64    `gorm:"type:bigint;not null" json:"chain_id"`
	ProtocolType uint16    `gorm:"type:smallint;not null" json:"protocol_type"`
	PoolAddress  string    `gorm:"type:varchar(64);not null" json:"pool_address"`
	Token0       string    `gorm:"type:varchar(64);not null" json:"token0"`
	Token1       string    `gorm:"type:varchar(64);not null" json:"token1"`
	Reserve0     string    `gorm:"type:varchar(100);not null" json:"reserve0"`
	Reserve1     string    `gorm:"type:varchar(100);not null" json:"reserve1"`
	Fee          uint16    `gorm:"type:smallint;not null" json:"fee"`
	TickSpacing  uint16    `gorm:"type:smallint;not null" json:"tick_spacing"`
	CreateTime   time.Time `gorm:"column:create_time;type:DATETIME;not null;default:CURRENT_TIMESTAMP;autoCreateTime" json:"create_time"` // 关键：数据库维护字段指定 autoCreateTime
	UpdateTime   time.Time `gorm:"column:update_time;type:DATETIME;not null;default:CURRENT_TIMESTAMP" json:"update_time"`                // 关键：数据库自己维护更新时间
}

func (PoolRecord) TableName() string { return "pool" }

type PoolModel struct{ BaseModel }

var (
	Pool PoolModel
)

// 插入一条
func CreatePool(poolRecord *PoolRecord) error {

	return Pool.DB().Create(poolRecord).Error
}

// 取一条pool
func GetOnePool(chainId uint64, protocolType uint16, token0 string, token1 string, fee uint16) ([]*PoolRecord, error) {

	query := Pool.DB().Table(PoolRecord{}.TableName())

	if chainId == 0 {
		return nil, errors.New("chainId=0")
	} else {
		query = query.Where("chain_id = ?", chainId)
	}
	if protocolType == 0 {
		return nil, errors.New("protocolType=0")
	} else {
		query = query.Where("protocol_type = ?", protocolType)
	}
	if token0 == "" {
		return nil, errors.New("token0为空")
	} else {
		query = query.Where("token0 = ?", token0)
	}
	if token1 == "" {
		return nil, errors.New("token1为空")
	} else {
		query = query.Where("token1 = ?", token1)
	}
	if fee != 0 {
		query = query.Where("fee = ?", fee)
	}

	var records []*PoolRecord
	err := query.Order("id desc").Offset(0).Limit(1).Scan(&records).Error
	if err != nil {
		return nil, err
	}

	return records, nil
}
