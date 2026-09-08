// Package repository 负责 order 服务的数据访问，与其他服务同一模式：
// GORM 细节隔离在这一层，service 只面向接口编程（单测用 mockery 替身）。
package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	apperrors "go-ecom-admin/pkg/errors"

	"go-ecom-admin/internal/order/model"
)

type Repository interface {
	// Create 落订单主表 + 明细行（同一个事务，见实现注释）。
	Create(ctx context.Context, o *model.Order) error
	GetByID(ctx context.Context, id uint64) (*model.Order, error)
	// ListByUser 只查某个用户自己的订单——订单是私密资源，
	// 不存在"公开列表"这种形态，查询入口天生带 user_id 过滤。
	ListByUser(ctx context.Context, userID uint64, page, pageSize int) ([]*model.Order, int64, error)
	// UpdateStatus 原子状态迁移：仅当当前状态等于 from 时才改成 to。
	UpdateStatus(ctx context.Context, id uint64, from, to string) error
}

type gormRepository struct {
	db *gorm.DB
}

func NewGormRepository(db *gorm.DB) Repository {
	return &gormRepository{db: db}
}

// Create 利用 GORM 的关联创建：orders + order_items 在同一个事务里落库，
// 任何一个失败整体回滚——订单和它的明细是同一个聚合，不允许出现
// "有订单没明细"的中间态。
func (r *gormRepository) Create(ctx context.Context, o *model.Order) error {
	if err := r.db.WithContext(ctx).Create(o).Error; err != nil {
		return apperrors.Internal("failed to create order", err)
	}
	return nil
}

func (r *gormRepository) GetByID(ctx context.Context, id uint64) (*model.Order, error) {
	var o model.Order
	if err := r.db.WithContext(ctx).Preload("Items").First(&o, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("order not found", err)
		}
		return nil, apperrors.Internal("failed to query order", err)
	}
	return &o, nil
}

// ListByUser 用最朴素的 offset/limit 分页（与 product 一致的理由）。
// 明细不 Preload：列表页只需要主表字段，N+1 次明细查询是纯浪费；
// 要看明细调 GetByID。
func (r *gormRepository) ListByUser(ctx context.Context, userID uint64, page, pageSize int) ([]*model.Order, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}

	var orders []*model.Order
	var total int64

	db := r.db.WithContext(ctx).Model(&model.Order{}).Where("user_id = ?", userID)
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, apperrors.Internal("failed to count orders", err)
	}

	offset := (page - 1) * pageSize
	if err := db.Order("id DESC").Offset(offset).Limit(pageSize).Find(&orders).Error; err != nil {
		return nil, 0, apperrors.Internal("failed to list orders", err)
	}

	return orders, total, nil
}

// UpdateStatus 用条件更新做原子状态迁移：
// UPDATE orders SET status = to WHERE id = ? AND status = from。
// 和 DeductStock 同一个思想——状态迁移的"检查当前状态 + 改写"压进一条 SQL，
// 没有 TOCTOU 窗口：两个并发取消（用户双击、重试）只有一个能命中
// RowsAffected == 1，另一个 RowsAffected == 0 得到 FailedPrecondition。
// service 层已先 GetByID 做过存在性/归属/状态检查，这里 RowsAffected == 0
// 的唯一解释是"检查和迁移之间状态被别人改了"，所以直接映射 FailedPrecondition。
func (r *gormRepository) UpdateStatus(ctx context.Context, id uint64, from, to string) error {
	result := r.db.WithContext(ctx).Model(&model.Order{}).
		Where("id = ? AND status = ?", id, from).
		Update("status", to)
	if result.Error != nil {
		return apperrors.Internal("failed to update order status", result.Error)
	}
	if result.RowsAffected == 0 {
		return apperrors.FailedPrecondition("order status changed concurrently", nil)
	}
	return nil
}
