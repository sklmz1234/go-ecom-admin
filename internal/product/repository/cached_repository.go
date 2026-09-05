package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	apperrors "go-ecom-admin/pkg/errors"

	"go-ecom-admin/internal/product/model"
)

// 缓存三问的答案（路线图要求写进注释）：
//  1. 不用它会坏什么？商品详情是读多写少场景，每个读请求都打到 MySQL，
//     高并发下连接池和行锁先被打爆——不是查不出来，是并发查不动。
//  2. 用它的代价是什么？写后到下次回源之间有脏读窗口（用 TTL 兜底），
//     以及多一个组件的运维成本。
//  3. 什么场景下该去掉它？低频后台管理操作占多数、没有热点读时，
//     缓存命中率低到不值得维护——缓存是为热点读服务的。
const (
	// productCachePrefix key 前缀带业务名，多个服务共用一个 Redis 实例时不撞 key。
	productCachePrefix = "product:"
	// productCacheTTL 正常缓存 30 分钟。防雪崩的进阶做法是 TTL 加随机抖动
	// （30min ± 5min），当前单服务单业务 key 量级下先不加，留作面试讲法。
	productCacheTTL = 30 * time.Minute
	// nullCacheTTL 空值缓存（防穿透）：查不存在的 id 时也缓存一个占位符，
	// 但 TTL 只给 1 分钟——真被刷不存在的 id 时占位 key 会占内存，
	// 短 TTL 让它们快速自然淘汰。
	nullCacheTTL = time.Minute
	// nullValue 空值占位符。用固定字符串而不是空串，语义更明确。
	nullValue = "__null__"
)

// cachedRepository 是 Repository 的缓存装饰器：实现同一个接口，内部包住
// 真实的 gormRepository。service 层完全感知不到缓存的存在（New 的时候传哪个
// 实现，service 就用哪个）——这就是面向接口编程的回报：加缓存不动业务代码。
type cachedRepository struct {
	next Repository // 被装饰的下一层（gorm 实现），命名 next 强调"调用链"语义
	rdb  *redis.Client
	// sf 防击穿：热点 key 过期的瞬间，同一个 key 的并发回源请求只放行第一个
	// 去查 MySQL，其余阻塞等待并共享它的结果（single = 单次，flight = 在途请求）。
	sf singleflight.Group
}

// NewCachedRepository 用 Redis 包装一个已有 Repository，返回的仍是 Repository 接口。
func NewCachedRepository(next Repository, rdb *redis.Client) Repository {
	return &cachedRepository{next: next, rdb: rdb}
}

func productKey(id uint64) string {
	return fmt.Sprintf("%s%d", productCachePrefix, id)
}

// GetByID 走标准 cache-aside 读路径：先查缓存 → 命中返回；未命中回源并回填。
// 两个特殊分支：
//   - 命中空值占位符 → 直接返回 NotFound，不打 DB（防穿透）；
//   - Redis 本身故障 → 降级直连 DB（缓存是加速层不是正确性依赖，
//     Redis 挂了顶多变慢，不能变成全站 500）。
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
		// 反序列化失败说明缓存内容损坏，当作未命中回源（不删 key，等 TTL 或下次写覆盖）。
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
			_ = r.rdb.Set(ctx, key, data, productCacheTTL).Err()
		}
		return p, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*model.Product), nil
}

// Create 直接透传：新商品没有热点，不回填缓存（读了再填，lazy 策略）。
func (r *cachedRepository) Create(ctx context.Context, p *model.Product) error {
	return r.next.Create(ctx, p)
}

// Update 写后失效：先写 MySQL（权威数据源），再删缓存 key。
// 为什么删而不是改缓存：删除是幂等的，两个并发写不会互相覆盖出旧值；
// 下次读未命中自然回源拿到最新值。DEL 失败只影响一致性窗口长短，返回错误让上层知道。
func (r *cachedRepository) Update(ctx context.Context, p *model.Product) error {
	if err := r.next.Update(ctx, p); err != nil {
		return err
	}
	if err := r.rdb.Del(ctx, productKey(p.ID)).Err(); err != nil {
		return apperrors.Internal("failed to invalidate product cache", err)
	}
	return nil
}

// Delete 同理：先删库再删缓存。顺序反过来（先删缓存）会有窗口期——
// 删缓存后、删库前的读请求会把旧数据重新回填，所以必须先库后缓存。
func (r *cachedRepository) Delete(ctx context.Context, id uint64) error {
	if err := r.next.Delete(ctx, id); err != nil {
		return err
	}
	if err := r.rdb.Del(ctx, productKey(id)).Err(); err != nil {
		return apperrors.Internal("failed to invalidate product cache", err)
	}
	return nil
}

// List 不缓存：分页列表的组合太多（page × pageSize），命中率低且
// 全量失效成本高（任何商品变更都要清所有页）。缓存只服务单条热点读。
func (r *cachedRepository) List(ctx context.Context, page, pageSize int) ([]*model.Product, int64, error) {
	return r.next.List(ctx, page, pageSize)
}

// isNotFound 按项目 errors 包的惯例判断 NotFound 语义：
// errors.As 解出 AppError 再比 Code（errors 包没有暴露哨兵错误，这是项目定的模式）。
func isNotFound(err error) bool {
	var appErr *apperrors.AppError
	return errors.As(err, &appErr) && appErr.Code == apperrors.CodeNotFound
}
