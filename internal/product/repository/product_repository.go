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
	Update(ctx context.Context, p *model.Product) error
	Delete(ctx context.Context, id uint64) error
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

// Update 用 RowsAffected 判断"有没有更新到东西"，而不是先 GetByID 查一遍
// 存在性再 Save——一次 UPDATE 语句就能同时完成"存在性检查 + 更新"，省一次
// 数据库往返，且天然没有 TOCTOU 竞态（检查和更新之间数据被删掉的问题）。
func (r *gormRepository) Update(ctx context.Context, p *model.Product) error {
	result := r.db.WithContext(ctx).Model(&model.Product{}).Where("id = ?", p.ID).Updates(map[string]any{
		"name":        p.Name,
		"price_cents": p.PriceCents,
		"stock":       p.Stock,
	})
	if result.Error != nil {
		return apperrors.Internal("failed to update product", result.Error)
	}
	if result.RowsAffected == 0 {
		return apperrors.NotFound("product not found", nil)
	}
	return nil
}

// Delete 同样靠 RowsAffected 判断目标是否存在，理由和 Update 一致。
func (r *gormRepository) Delete(ctx context.Context, id uint64) error {
	result := r.db.WithContext(ctx).Delete(&model.Product{}, id)
	if result.Error != nil {
		return apperrors.Internal("failed to delete product", result.Error)
	}
	if result.RowsAffected == 0 {
		return apperrors.NotFound("product not found", nil)
	}
	return nil
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
