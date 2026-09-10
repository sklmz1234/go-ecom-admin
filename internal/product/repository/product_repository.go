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
	// 阶段 3：库存扣减/回补。两个方法都返回操作后的商品（调用方需要
	// remaining stock；DeductStock 的调用方还要 price/name 做下单快照）。
	DeductStock(ctx context.Context, productID uint64, quantity int32) (*model.Product, error)
	RestoreStock(ctx context.Context, productID uint64, quantity int32) (*model.Product, error)
	// RestoreStockIdempotent（阶段 4）：message_id 去重的幂等回补，
	// 供 outbox relay 的至少一次投递使用——重复投递只生效一次。
	RestoreStockIdempotent(ctx context.Context, messageID string, productID uint64, quantity int32) (*model.Product, error)
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
		// 阶段 5A：整体替换语义必须覆盖全部可写字段，否则前端改图/描述会静默不生效。
		// owner_id 依然不在其中——归属不可转让。
		"description": p.Description,
		"image_url":   p.ImageURL,
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

// DeductStock 是防超卖的核心：把 CAS 下推到数据库，一条条件更新 SQL 原子完成
// "检查库存够不够 + 扣减"——UPDATE products SET stock = stock - n
// WHERE id = ? AND stock >= n。RowsAffected == 0 就是没抢到（库存不足或商品
// 不存在），不存在"先 SELECT 再 UPDATE"的 TOCTOU 窗口，数据库是唯一真相源。
//
// RowsAffected == 0 无法区分"商品不存在"和"库存不足"，而客户端需要不同的
// 错误码（404 vs 409），所以失败时补一次存在性检查——和 UpdateProduct 前多查
// 一次归属是同一个取舍：语义准确性值这一次查询，且失败路径不是热点。
func (r *gormRepository) DeductStock(ctx context.Context, productID uint64, quantity int32) (*model.Product, error) {
	result := r.db.WithContext(ctx).Model(&model.Product{}).
		Where("id = ? AND stock >= ?", productID, quantity).
		Update("stock", gorm.Expr("stock - ?", quantity))
	if result.Error != nil {
		return nil, apperrors.Internal("failed to deduct stock", result.Error)
	}
	if result.RowsAffected == 0 {
		var count int64
		if err := r.db.WithContext(ctx).Model(&model.Product{}).Where("id = ?", productID).Count(&count).Error; err != nil {
			return nil, apperrors.Internal("failed to check product existence", err)
		}
		if count == 0 {
			return nil, apperrors.NotFound("product not found", nil)
		}
		return nil, apperrors.FailedPrecondition("insufficient stock", nil)
	}

	// 读回更新后的完整记录：调用方需要 remaining stock，order-service 还需要
	// price_cents/name 做下单时刻的快照（见 proto DeductStockResponse 的注释）。
	return r.GetByID(ctx, productID)
}

// RestoreStock 是 DeductStock 的补偿操作：无条件加回库存。
// 它不幂等（重复调用会重复加），幂等性由调用方（order-service 的编排逻辑）
// 保证。消息驱动的回补（可能重复投递）应该走 RestoreStockIdempotent。
func (r *gormRepository) RestoreStock(ctx context.Context, productID uint64, quantity int32) (*model.Product, error) {
	result := r.db.WithContext(ctx).Model(&model.Product{}).
		Where("id = ?", productID).
		Update("stock", gorm.Expr("stock + ?", quantity))
	if result.Error != nil {
		return nil, apperrors.Internal("failed to restore stock", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, apperrors.NotFound("product not found", nil)
	}
	return r.GetByID(ctx, productID)
}

// RestoreStockIdempotent 是消息驱动回补的幂等版本（阶段 4）：
// 去重表 INSERT 和库存 +n 在同一个事务里。INSERT 撞主键（message_id 已存在）
// 说明这笔回补已生效过——不加库存、直接返回成功，把 relay 的"至少一次投递"
// 收敛成"恰好一次生效"。
//
// 为什么去重和生效必须原子：若分两步，"加了库存但去重记录没写上"的崩溃
// 窗口会让重投再补一次（库存虚增）；反过来"写了去重记录但没加库存"会让
// 重投被误判为已处理（库存永久少计）。同事务让两种窗口都不存在。
//
// 商品不存在时返回 NotFound 并整体回滚——去重记录也不留：回补没生效，
// 就不该冒充"已处理"，relay 会按退避重试直到 DEAD 死信。
func (r *gormRepository) RestoreStockIdempotent(ctx context.Context, messageID string, productID uint64, quantity int32) (*model.Product, error) {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&model.StockRestore{
			MessageID: messageID,
			ProductID: productID,
			Quantity:  quantity,
		}).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				// 已处理过：事务里什么都不做直接提交（空事务），
				// 幂等语义=重复调用与第一次调用的可观察结果一致。
				return nil
			}
			return apperrors.Internal("failed to record stock restore", err)
		}
		result := tx.Model(&model.Product{}).
			Where("id = ?", productID).
			Update("stock", gorm.Expr("stock + ?", quantity))
		if result.Error != nil {
			return apperrors.Internal("failed to restore stock", result.Error)
		}
		if result.RowsAffected == 0 {
			return apperrors.NotFound("product not found", nil)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// 无论本次是真回补还是去重命中，读回当前库存返回即可——
	// 调用方（relay）只关心"成功了没有"，remaining 用于日志。
	return r.GetByID(ctx, productID)
}
