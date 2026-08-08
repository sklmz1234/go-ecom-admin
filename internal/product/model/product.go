// Package model 存放 product 服务的持久化模型（GORM）。
package model

import "time"

// Product 对应 MySQL 中的 products 表。
// 价格用 PriceCents（分）存储，和 proto 里的约定一致，全链路不出现浮点价格。
type Product struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement"`
	Name       string    `gorm:"column:name;type:varchar(128);not null"`
	PriceCents int64     `gorm:"column:price_cents;not null"`
	Stock      int32     `gorm:"column:stock;not null;default:0"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt  time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (Product) TableName() string {
	return "products"
}
