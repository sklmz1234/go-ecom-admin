// Package database 提供数据库相关的横切能力。目前只有带锁迁移 Migrate：
// 根治多副本（K8s Deployment 2 副本 / docker compose scale）同时启动时
// AutoMigrate 抢建表的竞态。
//
// 为什么会竞态：AutoMigrate 是"查表不存在则 CREATE，存在则比对 ALTER"，
// 两个副本同时查到 users 不存在 → 都去 CREATE → 后到的报
// Error 1050 Table 'users' already exists → Fatal → CrashLoopBackOff。
// 之前在 K8s 集群里实测出现过一轮 CrashLoop 后自愈，但每次滚动更新
// 两个副本同时起时都是赌博，不值得留。
//
// 为什么用 MySQL 命名锁（GET_LOCK）而不是别的方案：
//   - Redis 分布式锁：为了一年少见几次的建表竞态引入 Redis 依赖，
//     在"本地 go run / docker compose / K8s"三种运行形态下都得多起
//     一个组件，成本远大于收益。
//   - 拆独立迁移 Job / initContainer：是教科书做法，但要求本地开发和
//     compose 也先跑一遍迁移才能起服务，破坏"go run main.go 直接能跑"
//     的开发体验。锁方案的语义是"谁先抢到谁建表，后来者等一下再跑
//     AutoMigrate（幂等，表已存在直接过）"，三种形态零额外步骤。
//   - GET_LOCK 是连接级的命名锁：同一连接内名字互斥，不同连接抢同名
//     锁会阻塞。RELEASE_LOCK 在 defer 里释放，连接归还连接池后锁也
//     会随连接断开自动释放，不会死锁残留。
//
// wait 语义：抢不到锁最多等 wait（本项内各入口传 30s）。正常情况下
// AutoMigrate 毫秒级完成，30s 足够覆盖"另一个副本正在建全部表"的窗口；
// 超时则报错退出交给 K8s 重启——宁可慢，不要并发写 schema。
package database

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// migrateLockName 是全局唯一的锁名：user-service / product-service / seed
// 三个入口共用同一把锁，保证任何时刻整个系统只有一个 AutoMigrate 在跑。
const migrateLockName = "go_ecom_admin_schema_migrate"

// Migrate 在持有 MySQL 命名锁的前提下执行 AutoMigrate，串行化多副本启动。
func Migrate(db *gorm.DB, wait time.Duration, models ...any) error {
	var got int
	// GET_LOCK(name, timeout) 返回 1=抢到 0=超时。
	if err := db.Raw("SELECT GET_LOCK(?, ?)", migrateLockName, int(wait.Seconds())).Scan(&got).Error; err != nil {
		return fmt.Errorf("acquire migrate lock: %w", err)
	}
	if got != 1 {
		return fmt.Errorf("acquire migrate lock: timed out after %s (another instance is migrating?)", wait)
	}
	defer func() {
		// 释放失败只忽略：锁随连接关闭自动释放，最坏情况是
		// 后来者多等一会，不影响正确性。
		_ = db.Exec("SELECT RELEASE_LOCK(?)", migrateLockName).Error
	}()

	if err := db.AutoMigrate(models...); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}
	return nil
}
