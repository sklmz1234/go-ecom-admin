// outbox_repository.go：本地消息表（阶段 4）的数据访问。
//
// 它和订单 CRUD 的 Repository 拆成两个接口：outbox 的 Claim 是
// "打开事务 → 锁行 → 把事务句柄交出去 → 调用方收尾"的长事务会话模式，
// 和 Create/GetByID 这种一问一答的短操作形态完全不同——混在一个
// 接口里会让订单 repo 的实现和 mock 都背上不必要的负担。
package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	apperrors "go-ecom-admin/pkg/errors"

	"go-ecom-admin/internal/order/model"
)

// MaxOutboxRetry 是重试上限：达到后消息进 DEAD 死信，不再自动投递。
// 死信不是失败终点而是"换人处理"的信号——学习项目留 ERROR 日志 +
// 人工改库重放（重放工具是 4b 的选做）。
const MaxOutboxRetry = 10

// OutboxRepository 是本地消息表的 repo 契约。
type OutboxRepository interface {
	// CancelWithOutbox 一个事务完成"条件状态迁移 + 批量写 outbox 消息"：
	// 状态迁移命中（RowsAffected==1）则消息必落库，迁移不命中则消息必不落——
	// outbox 模式的原子性锚点。并发取消（双击/重试）只有一个事务能命中，
	// 其余整体回滚返回 FailedPrecondition，不存在"取消成功但消息丢失"的窗口。
	CancelWithOutbox(ctx context.Context, orderID uint64, from, to string, messages []*model.OutboxMessage) error
	// ClaimPending 抢一批到期待投递消息（事务内 FOR UPDATE SKIP LOCKED，
	// 行锁保持到 session 收尾），没有可投递消息时返回 nil session。
	ClaimPending(ctx context.Context, limit int) (*OutboxSession, error)
	// CountPending 统计 PENDING 积压量（含退避等待中的），供 relay 指标上报。
	CountPending(ctx context.Context) (int64, error)
}

// OutboxSession 是一次 Claim 拿到的"持锁会话"：ClaimPending 里打开的
// 事务（连同行锁）由它持有，relay 逐条投递后用 MarkSent/MarkFailed 在
// 同一事务里改状态，最后 Close 提交。
//
// 为什么锁要横跨投递过程：投递期间（最长一个 gRPC 超时 3s）其他副本的
// Claim 会被 SKIP LOCKED 跳过这些行，同一消息不会被两个副本同时投递
// （数据库层的分片）。代价是投递期间持有行锁——3s 上限内可接受，
// 换来的是不需要引入"PROCESSING 中间态 + 租约"的额外复杂度。
//
// 崩溃安全：relay 进程若在投递中途死掉，事务未提交、连接断开自动回滚，
// 消息回到 PENDING 等下一轮——"至少一次"语义的一部分。
type OutboxSession struct {
	// Messages 是本轮抢到的消息（按 id 升序，FIFO 投递）。
	Messages []model.OutboxMessage
	tx       *gorm.DB
}

// MarkSent 标记投递成功（SENT + sent_at）。行锁在手上，直接按 id 更新。
func (s *OutboxSession) MarkSent(ctx context.Context, id uint64) error {
	if s.tx == nil {
		return apperrors.Internal("outbox session already closed", nil)
	}
	err := s.tx.WithContext(ctx).Model(&model.OutboxMessage{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":  model.OutboxStatusSent,
			"sent_at": time.Now(),
		}).Error
	if err != nil {
		return apperrors.Internal("failed to mark outbox message sent", err)
	}
	return nil
}

// MarkFailed 记录一次投递失败：retry_count+1、按指数退避推远 next_retry_at
// （2^retry 秒，封顶 5 分钟）；达到 MaxOutboxRetry 则置 DEAD 死信。
// 返回 dead=true 提示调用方记 ERROR 日志（repo 层不持有 logger）。
func (s *OutboxSession) MarkFailed(ctx context.Context, id uint64, cause error) (dead bool, err error) {
	if s.tx == nil {
		return false, apperrors.Internal("outbox session already closed", nil)
	}
	// retry_count 取自 Claim 时读到的内存值——行锁在手上，读后无人能改，
	// 不需要再查一次库。
	var claimed *model.OutboxMessage
	for i := range s.Messages {
		if s.Messages[i].ID == id {
			claimed = &s.Messages[i]
			break
		}
	}
	if claimed == nil {
		return false, apperrors.Internal("outbox message not in this session", nil)
	}

	newRetry := claimed.RetryCount + 1
	updates := map[string]any{"retry_count": newRetry}

	if newRetry >= MaxOutboxRetry {
		updates["status"] = model.OutboxStatusDead
		dead = true
	} else {
		// 指数退避：2^retry 秒封顶 5 分钟（1 次失败 2s、2 次 4s……
		// 8 次 256s、9 次 300s）。退避的意义：对端故障时把重试频率
		// 从"每 2s 撞一次墙"衰减下来，给恢复留窗口，也少打日志。
		backoff := time.Duration(1<<newRetry) * time.Second
		if backoff > 5*time.Minute {
			backoff = 5 * time.Minute
		}
		updates["next_retry_at"] = time.Now().Add(backoff)
	}

	if err := s.tx.WithContext(ctx).Model(&model.OutboxMessage{}).
		Where("id = ?", id).
		Updates(updates).Error; err != nil {
		return false, apperrors.Internal("failed to mark outbox message failed", err)
	}
	return dead, nil
}

// MarkDead 直接置 DEAD（跳过退避计数），给"重试也不会好"的永久性失败
// （消息体损坏、payload 语义非法）用；普通投递失败走 MarkFailed 的
// 退避路径——网络故障是暂时的，不该陪葬。
func (s *OutboxSession) MarkDead(ctx context.Context, id uint64) error {
	if s.tx == nil {
		return apperrors.Internal("outbox session already closed", nil)
	}
	err := s.tx.WithContext(ctx).Model(&model.OutboxMessage{}).
		Where("id = ?", id).
		Update("status", model.OutboxStatusDead).Error
	if err != nil {
		return apperrors.Internal("failed to mark outbox message dead", err)
	}
	return nil
}

// Close 提交会话事务（标记过的 SENT/FAILED/DEAD 一并生效），幂等可重入。
// 前置的 claim 查询不产生写，所以"没有标记任何消息就 Close"是合法的
// 空提交。提交失败时事务已回滚，消息回到 PENDING——at-least-once。
func (s *OutboxSession) Close() error {
	if s.tx == nil {
		return nil
	}
	err := s.tx.Commit().Error
	s.tx = nil
	return err
}

// ClaimPending 的实现：手动 Begin（不走 gorm.Transaction——它在回调
// 返回时就提交/回滚，会把行锁提前放掉，会话模式必须自己管事务生命周期）。
func (r *gormRepository) ClaimPending(ctx context.Context, limit int) (*OutboxSession, error) {
	tx := r.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return nil, apperrors.Internal("failed to begin outbox claim tx", tx.Error)
	}

	query := tx.Model(&model.OutboxMessage{}).
		Where("status = ? AND next_retry_at <= ?", model.OutboxStatusPending, time.Now()).
		Order("id ASC").
		Limit(limit)

	// FOR UPDATE SKIP LOCKED 只在 MySQL 8.0+ 存在（这正是当初选 MySQL 8.0
	// 的回报之一）；sqlite（单测）没有多副本并发的场景，跳过锁子句照样
	// 测得到状态机。方言守卫而不是 try-error：SQL 方言差异应该显式声明。
	if tx.Dialector.Name() == "mysql" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
	}

	var msgs []model.OutboxMessage
	if err := query.Find(&msgs).Error; err != nil {
		tx.Rollback()
		return nil, apperrors.Internal("failed to claim outbox messages", err)
	}
	if len(msgs) == 0 {
		// 本轮无可投递：回滚结束事务（空事务提交也一样，回滚更省一次 fsync 语义）。
		tx.Rollback()
		return nil, nil
	}
	return &OutboxSession{Messages: msgs, tx: tx}, nil
}

// NewOutboxRepository 与 NewGormRepository 包的是同一个 gormRepository——
// 拆开两个构造函数只是让两个接口各自的装配意图清晰（order repo 管
// 订单聚合，outbox repo 管消息表），实现层共享连接池和迁移路径。
func NewOutboxRepository(db *gorm.DB) OutboxRepository {
	return &gormRepository{db: db}
}

func (r *gormRepository) CancelWithOutbox(ctx context.Context, orderID uint64, from, to string, messages []*model.OutboxMessage) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.Order{}).
			Where("id = ? AND status = ?", orderID, from).
			Update("status", to)
		if result.Error != nil {
			return apperrors.Internal("failed to update order status", result.Error)
		}
		// 迁移不命中（状态被并发改掉）：整个事务回滚，一条消息都不写。
		if result.RowsAffected == 0 {
			return apperrors.FailedPrecondition("order status changed concurrently", nil)
		}
		if len(messages) == 0 {
			return nil
		}
		if err := tx.Create(&messages).Error; err != nil {
			return apperrors.Internal("failed to write outbox messages", err)
		}
		return nil
	})
}

func (r *gormRepository) CountPending(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&model.OutboxMessage{}).
		Where("status = ?", model.OutboxStatusPending).
		Count(&n).Error; err != nil {
		return 0, apperrors.Internal("failed to count pending outbox messages", err)
	}
	return n, nil
}
