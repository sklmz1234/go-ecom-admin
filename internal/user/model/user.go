// Package model 存放 user 服务的持久化模型（GORM）。
package model

import "time"

// User 对应 MySQL 中的 users 表。
//
// 注意它和 proto/user.User（gRPC 传输消息）是两个独立的类型：
// 这里可以自由增加 CreatedAt/UpdatedAt/DeletedAt 等持久化专用字段，
// 而不会影响对外暴露的 gRPC 契约。两者的转换写在 service 层。
type User struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	Username  string    `gorm:"column:username;type:varchar(64);uniqueIndex;not null"`
	Email     string    `gorm:"column:email;type:varchar(128);uniqueIndex;not null"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName 显式指定表名，避免依赖 GORM 的复数命名推断（表名以后改了
// 命名规则也不会因为 GORM 版本升级而突然对不上）。
func (User) TableName() string {
	return "users"
}
