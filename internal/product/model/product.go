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
	// OwnerID 是创建者的用户 ID，由 service 层从 gRPC metadata 的 user_id 写入，
	// 客户端无法指定或修改（repo 的 Update 只更新 name/price/stock 三个字段，
	// 归属不可转让——"转让商品"如果未来需要，应该是显式的业务操作）。
	// 加索引是因为"列出我发布的商品"是归属模型的自然延伸查询。
	// 存量数据迁移后为 0，语义是"无主"：任何人都不能改/删（不存在 uid=0 的用户），
	// 本地开发直接重跑 seed 即可拿到带归属的数据。
	OwnerID    uint64    `gorm:"column:owner_id;not null;default:0;index"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt  time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (Product) TableName() string {
	return "products"
}
