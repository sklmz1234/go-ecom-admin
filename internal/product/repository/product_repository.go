// Package repository 负责 product 服务的数据访问，理由与 internal/user/repository 一致：
// 把 GORM 相关细节隔离在这一层，service 只面向接口编程。
package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	apperrors "go-ecom-admin/pkg/errors"

	"go-ecom-admin/internal/product/model"
)

type Repository interface {
	Create(ctx context.Context, p *model.Product) error
	GetByID(ctx context.Context, id uint64) (*model.Product, error)
	List(ctx context.Context, page, pageSize int) ([]*model.Product, int64, error)
}

type gormRepository struct {
	db *gorm.DB
}

func NewGormRepository(db *gorm.DB) Repository {
	return &gormRepository{db: db}
}

func (r *gormRepository) Create(ctx context.Context, p *model.Product) error {
	if err := r.db.WithContext(ctx).Create(p).Error; err != nil {
		return apperrors.Internal("failed to create product", err)
	}
	return nil
}

func (r *gormRepository) GetByID(ctx context.Context, id uint64) (*model.Product, error) {
	var p model.Product
	if err := r.db.WithContext(ctx).First(&p, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("product not found", err)
		}
		return nil, apperrors.Internal("failed to query product", err)
	}
	return &p, nil
}

// List 用最朴素的 offset/limit 分页——阶段 1 数据量小，等以后接入真实业务
// 再按需要换成游标分页，不提前做过度设计。
func (r *gormRepository) List(ctx context.Context, page, pageSize int) ([]*model.Product, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}

	var products []*model.Product
	var total int64

	db := r.db.WithContext(ctx).Model(&model.Product{})
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, apperrors.Internal("failed to count products", err)
	}

	offset := (page - 1) * pageSize
	if err := db.Offset(offset).Limit(pageSize).Find(&products).Error; err != nil {
		return nil, 0, apperrors.Internal("failed to list products", err)
	}

	return products, total, nil
}
