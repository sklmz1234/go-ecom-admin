// Package model 存放 order 服务的持久化模型（GORM）。
package model

import "time"

// 订单状态机：创建即 PENDING；Cancel 只允许 PENDING → CANCELLED 单方向流转
// （阶段 3b 实现）；PAID 留给支付环节。用 string 存储而不是 int——
// 数据库里直接可读（排查时不用对着 0/1/2 猜语义），与 proto 枚举的映射
// 收拢在 service 层一处。
const (
	StatusPending   = "PENDING"
	StatusPaid      = "PAID"
	StatusCancelled = "CANCELLED"
)

// Order 对应 MySQL 中的 orders 表。
// UserID 加索引："列出我的订单"（ListMyOrders）是订单域的头号查询。
// TotalCents 落库冗余——它等于 items 的 quantity × unit_price_cents 之和，
// 但列表页展示订单金额不该每次都 join 聚合，这是用存储换查询的简单选择。
type Order struct {
	ID         uint64      `gorm:"primaryKey;autoIncrement"`
	UserID     uint64      `gorm:"column:user_id;not null;index"`
	Status     string      `gorm:"column:status;type:varchar(16);not null"`
	TotalCents int64       `gorm:"column:total_cents;not null"`
	Items      []OrderItem `gorm:"foreignKey:OrderID"`
	CreatedAt  time.Time   `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt  time.Time   `gorm:"column:updated_at;autoUpdateTime"`
}

func (Order) TableName() string {
	return "orders"
}

// OrderItem 对应 order_items 表。ProductName / UnitPriceCents 是下单时刻的
// 快照：商品价格会改、名字会改、甚至商品会下架，但历史订单必须保持
// "用户当时买的是什么、多少钱"不变——这是电商订单模型的基本功。
type OrderItem struct {
	ID             uint64 `gorm:"primaryKey;autoIncrement"`
	OrderID        uint64 `gorm:"column:order_id;not null;index"`
	ProductID      uint64 `gorm:"column:product_id;not null"`
	ProductName    string `gorm:"column:product_name;type:varchar(128);not null"`
	Quantity       int32  `gorm:"column:quantity;not null"`
	UnitPriceCents int64  `gorm:"column:unit_price_cents;not null"`
}

func (OrderItem) TableName() string {
	return "order_items"
}
