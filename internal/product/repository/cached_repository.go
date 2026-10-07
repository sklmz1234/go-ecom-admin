package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	apperrors "go-ecom-admin/pkg/errors"

	"go-ecom-admin/internal/product/model"
)

const (
	// productCachePrefix key 前缀带业务名，多个服务共用一个 Redis 实例时不撞 key。
	productCachePrefix = "product:"

	productCacheTTL = 30 * time.Minute

	// cacheTTLJitter 防雪崩：若所有商品 key 用同一 TTL，同一批回填的 key 会在
	// 同一时刻集体过期，回源流量同时砸向 MySQL。回填时给 TTL 加 [0, 5min)
	// 随机增量，把过期时间打散到一段区间内。math/rand 即可，无需加密强度。
	cacheTTLJitter = 5 * time.Minute

	nullCacheTTL = time.Minute

	nullValue = "__null__"
)

type cachedRepository struct {
	next Repository // 被装饰的下一层（gorm 实现），命名 next 强调"调用链"语义
	rdb  *redis.Client

	sf singleflight.Group
}

// NewCachedRepository 用 Redis 包装一个已有 Repository，返回的仍是 Repository 接口。
func NewCachedRepository(next Repository, rdb *redis.Client) Repository {
	return &cachedRepository{next: next, rdb: rdb}
}

func productKey(id uint64) string {
	return fmt.Sprintf("%s%d", productCachePrefix, id)
}

// jitteredTTL 返回 base + [0, jitter) 的随机 TTL，见 cacheTTLJitter 注释。
func jitteredTTL(base, jitter time.Duration) time.Duration {
	return base + time.Duration(rand.Int63n(int64(jitter)))
}

func (r *cachedRepository) GetByID(ctx context.Context, id uint64) (*model.Product, error) {
	key := productKey(id)

	cached, err := r.rdb.Get(ctx, key).Result()
	if err == nil {
		if cached == nullValue {
			return nil, apperrors.NotFound("product not found", nil)
		}
		var p model.Product
		if json.Unmarshal([]byte(cached), &p) == nil {
			return &p, nil
		}

	} else if !errors.Is(err, redis.Nil) {
		// redis.Nil = key 不存在（正常未命中）；其他错误 = Redis 故障，降级直连。
		return r.next.GetByID(ctx, id)
	}

	// 未命中：singleflight 合并并发回源。fn 的返回值会共享给所有等待者。
	v, err, _ := r.sf.Do(key, func() (any, error) {
		p, err := r.next.GetByID(ctx, id)
		if err != nil {
			// 商品不存在 → 缓存空值占位符防穿透（短 TTL，理由见常量注释）。
			if isNotFound(err) {
				_ = r.rdb.Set(ctx, key, nullValue, nullCacheTTL).Err()
			}
			return nil, err
		}
		// 回填缓存。Set 失败不视为错误——下次读还会回源，只是这次没加速到。
		if data, mErr := json.Marshal(p); mErr == nil {
			_ = r.rdb.Set(ctx, key, data, jitteredTTL(productCacheTTL, cacheTTLJitter)).Err()
		}
		return p, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*model.Product), nil
}

// Create 直接透传
func (r *cachedRepository) Create(ctx context.Context, p *model.Product) error {
	return r.next.Create(ctx, p)
}

func (r *cachedRepository) Update(ctx context.Context, p *model.Product) error {
	if err := r.next.Update(ctx, p); err != nil {
		return err
	}
	if err := r.rdb.Del(ctx, productKey(p.ID)).Err(); err != nil {
		return apperrors.Internal("failed to invalidate product cache", err)
	}
	return nil
}

func (r *cachedRepository) Delete(ctx context.Context, id uint64) error {
	if err := r.next.Delete(ctx, id); err != nil {
		return err
	}
	if err := r.rdb.Del(ctx, productKey(id)).Err(); err != nil {
		return apperrors.Internal("failed to invalidate product cache", err)
	}
	return nil
}

func (r *cachedRepository) List(ctx context.Context, page, pageSize int) ([]*model.Product, int64, error) {
	return r.next.List(ctx, page, pageSize)
}

func (r *cachedRepository) SearchByKeyword(ctx context.Context, keyword string, page, pageSize int) ([]*model.Product, int64, error) {
	return r.next.SearchByKeyword(ctx, keyword, page, pageSize)
}

func (r *cachedRepository) ListByIDs(ctx context.Context, ids []uint64) ([]*model.Product, error) {
	return r.next.ListByIDs(ctx, ids)
}

func (r *cachedRepository) DeductStock(ctx context.Context, productID uint64, quantity int32) (*model.Product, error) {
	p, err := r.next.DeductStock(ctx, productID, quantity)
	if err != nil {
		return nil, err
	}
	if err := r.rdb.Del(ctx, productKey(productID)).Err(); err != nil {
		return nil, apperrors.Internal("failed to invalidate product cache", err)
	}
	return p, nil
}

func (r *cachedRepository) RestoreStock(ctx context.Context, productID uint64, quantity int32) (*model.Product, error) {
	p, err := r.next.RestoreStock(ctx, productID, quantity)
	if err != nil {
		return nil, err
	}
	if err := r.rdb.Del(ctx, productKey(productID)).Err(); err != nil {
		return nil, apperrors.Internal("failed to invalidate product cache", err)
	}
	return p, nil
}

// 幂等性在 gorm 层的去重表里，缓存层只管写后失效，不感知 message_id。
func (r *cachedRepository) RestoreStockIdempotent(ctx context.Context, messageID string, productID uint64, quantity int32) (*model.Product, error) {
	p, err := r.next.RestoreStockIdempotent(ctx, messageID, productID, quantity)
	if err != nil {
		return nil, err
	}
	if err := r.rdb.Del(ctx, productKey(productID)).Err(); err != nil {
		return nil, apperrors.Internal("failed to invalidate product cache", err)
	}
	return p, nil
}

// isNotFound 按项目 errors 包的惯例判断 NotFound 语义：

func isNotFound(err error) bool {
	var appErr *apperrors.AppError
	return errors.As(err, &appErr) && appErr.Code == apperrors.CodeNotFound
}
