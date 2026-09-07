// connect.go：带退避的数据库连接重试。
//
// 为什么需要它：Docker daemon 重启后，所有 restart: unless-stopped 的容器
// 会被**并行**拉起——compose 的 depends_on: service_healthy 只在
// `docker compose up` 编排时生效，daemon 恢复后的自动重启不走依赖检查
// （2026-09-07 实测：MySQL 还在初始化，user/product-service 已经 Fatal，
// 陷入崩溃循环，Docker 的重启退避最长拖到 1 分钟才再试一次）。
//
// 分层原则：
//   - 配置错误（config 加载失败、端口被占）→ 立即退出，重试没有意义；
//   - 基础设施未就绪（DNS 查不到、连接拒绝）→ 应用层带退避重试，
//     这是"很快自愈"的第一道防线；
//   - 重试超过 MaxWait 仍失败 → 返回错误，main Fatal 退出，交给
//     restart 策略 / K8s 重新拉起——这是"最终自愈"的兜底。
//     应用层重试秒级间隔，比容器重启的分钟级退避恢复快得多。
package database

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ConnectConfig 控制连接重试行为。零值可用：默认重试总时长 2 分钟，
// 退避从 1s 起指数增长、封顶 10s。
type ConnectConfig struct {
	// MaxWait 重试总时长上限，超过后返回最后一次错误。
	MaxWait time.Duration
	// MinDelay 第一次重试前的等待（也是退避基数）。
	MinDelay time.Duration
	// MaxDelay 单次等待的上限。
	MaxDelay time.Duration
	// Log 每次失败的 Warn 和最终成功的 Info 都写到这里；nil 则静默。
	Log *zap.Logger
}

func (c *ConnectConfig) fillDefaults() {
	if c.MaxWait <= 0 {
		c.MaxWait = 2 * time.Minute
	}
	if c.MinDelay <= 0 {
		c.MinDelay = time.Second
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = 10 * time.Second
	}
	if c.MaxDelay < c.MinDelay {
		c.MaxDelay = c.MinDelay
	}
}

// ConnectWithRetry 反复调用 open 直到成功、超时或 ctx 被取消。
// open 传函数而不是 DSN：调用方决定 gorm.Open 的具体参数
// （TranslateError 等），这里只管"何时重试"这个横切逻辑。
func ConnectWithRetry(ctx context.Context, open func() (*gorm.DB, error), cfg ConnectConfig) (*gorm.DB, error) {
	cfg.fillDefaults()
	deadline := time.Now().Add(cfg.MaxWait)
	delay := cfg.MinDelay

	for attempt := 1; ; attempt++ {
		db, err := open()
		if err == nil {
			if attempt > 1 && cfg.Log != nil {
				cfg.Log.Info("mysql connected after retries", zap.Int("attempts", attempt))
			}
			return db, nil
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("mysql unreachable after %d attempts over %s: %w", attempt, cfg.MaxWait, err)
		}

		wait := min(delay, remaining)
		if cfg.Log != nil {
			cfg.Log.Warn("mysql not ready, will retry",
				zap.Int("attempt", attempt),
				zap.Duration("retry_in", wait),
				zap.Error(err))
		}

		if !sleep(ctx, wait) {
			return nil, fmt.Errorf("mysql connect retry canceled (attempt %d): %w", attempt, err)
		}
		delay = min(delay*2, cfg.MaxDelay)
	}
}

// sleep 等待 d，期间 ctx 取消则返回 false。包级变量是为了测试里
// 替换成瞬时实现，避免单测真实睡眠拖慢 CI。
var sleep = func(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
