// Package cache 提供 Redis 客户端的构造与通用工具。
//
// 设计决策：
//  1. 和 pkg/database 对称：main.go 只负责"建连接"，缓存策略（key 怎么设计、
//     TTL 多少、失效怎么做）全部下沉到各服务的 repository 装饰器里——
//     pkg 层不出现任何业务 key，避免公共包变成杂货铺。
//  2. New 里只做 PING 不做隐式重试：连接失败快速失败（fail fast），
//     调用方决定是降级还是 Fatal，公共包不替业务做这个决策。
package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config 直接映射 pkg/config 的 RedisConfig，cache 包不感知 Viper 的存在——
// 和 logger.Config 的做法一致（依赖倒置：配置包依赖这里，这里不依赖配置包）。
type Config struct {
	Addr     string
	Password string
	DB       int
}

// New 建立 Redis 客户端并 PING 验证连通性。
// 返回的 *redis.Client 是并发安全的，全进程共享一个即可（内部连接池）。
func New(cfg Config) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("cache: ping redis %s: %w", cfg.Addr, err)
	}
	return rdb, nil
}
