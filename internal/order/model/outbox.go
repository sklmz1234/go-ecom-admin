// Package model 的 outbox.go：本地消息表模型（阶段 4）。
package model

import "time"

// OutboxMessage 对应 outbox_messages 表：本地消息表（transactional outbox，
// 阶段 4）的核心载体。
//
// 为什么需要它：取消订单的库存回补如果靠"改完状态再同步调 product-service"，
// 调用失败（网络/对端重启）时状态已迁移、回补却丢了——数据不一致只剩 ERROR
// 日志可查。本地消息表把"业务状态迁移"和"要做的事"压进同一个本地事务：
// 要么都落库，要么都没有，原子性由数据库事务保证；投递交给后台 relay
// 至少一次地重试，消费端（product 的去重表）把"至少一次"收敛成"恰好一次"。
//
// 生命周期：PENDING（写入即等投递）→ SENT（投递成功）/ DEAD（重试 10 次
// 仍失败，死信留人工处置——学习项目到 ERROR 日志为止，重放工具留 4b）。
type OutboxMessage struct {
	// ID 自增主键，同时是投递顺序依据（FIFO）。
	ID uint64 `gorm:"primaryKey;autoIncrement"`
	// MessageID 是业务幂等键（UUID），由 service 层在组装消息时生成，
	// 随 RestoreStockRequest 传给 product-service 做去重。加唯一索引：
	// 任何来源的重复写入（未来重放工具、人工补录）在库层就被挡住。
	MessageID string    `gorm:"column:message_id;type:varchar(36);not null;uniqueIndex"`
	Type      string    `gorm:"column:type;type:varchar(32);not null"`
	// Payload 存 JSON（product_id/quantity/order_id）：消息体不与表结构耦合，
	// 未来加新消息类型不用 ALTER。用 datatypes.JSON 语义上更准，
	// 但 string + json.Marshal 更直白，也不用引 gorm.io/datatypes 依赖。
	Payload    string    `gorm:"column:payload;type:json;not null"`
	Status     string    `gorm:"column:status;type:varchar(16);not null;index"`
	RetryCount int       `gorm:"column:retry_count;not null;default:0"`
	// NextRetryAt 是退避重试的"下次可投递时间"：Claim 只扫
	// next_retry_at <= NOW() 的行，失败后按指数退避推远。
	// sqlite/MySQL 都用 time.Time 映射 datetime。
	NextRetryAt time.Time `gorm:"column:next_retry_at;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime"`
	SentAt      *time.Time `gorm:"column:sent_at"`
}

func (OutboxMessage) TableName() string {
	return "outbox_messages"
}

// 消息状态机。status 用 string 存（与订单状态同一取舍：库里直接可读）。
const (
	OutboxStatusPending = "PENDING"
	OutboxStatusSent    = "SENT"
	OutboxStatusDead    = "DEAD"
)

// 消息类型。目前只有库存回补一种；用 type 字段而不是独立表，
// 是给"未来一事务多消息类型"留的扩展位（如发通知、清缓存）。
const (
	OutboxTypeStockRestore = "stock.restore"
)

// StockRestorePayload 是 OutboxTypeStockRestore 的消息体。
type StockRestorePayload struct {
	ProductID uint64 `json:"product_id"`
	Quantity  int32  `json:"quantity"`
	OrderID   uint64 `json:"order_id"`
}
