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
