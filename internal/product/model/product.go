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
	// 阶段 5A：C 端商城展示字段。ImageURL 存外链（不本地托管图片，零基建起步），
	// 空串=无图，前端兜底占位图。Description 给 1024 而不是 TEXT：
	// 列表页摘要和详情页展示都用它，电商详情富文本是以后独立字段/独立服务的事。
	Description string    `gorm:"column:description;type:varchar(1024);not null;default:''"`
	ImageURL    string    `gorm:"column:image_url;type:varchar(512);not null;default:''"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt  time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (Product) TableName() string {
	return "products"
}

// StockRestore 对应 stock_restores 表：消息驱动库存回补的去重表（阶段 4）。
// MessageID 是主键——同一 message_id 第二次 INSERT 必撞唯一约束，
// 冲突即"这笔回补已生效过"，直接返回成功不重复加库存。
// 它同时是一张可审计的回补流水：哪条消息、给哪个商品、补了多少、什么时候。
// 不设外键关联 products：商品可能被删除，但回补流水要留下来供对账。
type StockRestore struct {
	MessageID string    `gorm:"column:message_id;type:varchar(36);primaryKey"`
	ProductID uint64    `gorm:"column:product_id;not null"`
	Quantity  int32     `gorm:"column:quantity;not null"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (StockRestore) TableName() string {
	return "stock_restores"
}
