// Package outbox 承载本地消息表的投递端：一个内嵌在 order-service 里的
// relay goroutine，周期性扫描 outbox_messages 把到期的消息投递给
// product-service。
//
// 为什么放进程内而不是独立 worker 服务：与业务同进程零部署成本，
// 2 副本 order-service = 2 个 relay 实例，靠 ClaimPending 的
// FOR UPDATE SKIP LOCKED 天然分片互不冲突——这正是当初选 MySQL 8.0
// 的回报之一（决策见阶段 4 方案文档决策 2）。
package outbox

import (
	"context"
	"encoding/json"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"go-ecom-admin/internal/order/model"
	"go-ecom-admin/internal/order/repository"
	"go-ecom-admin/pkg/identity"
)

const (
	// DefaultInterval 是扫描周期：2s 意味着"取消订单到库存回补"的
	// 正常延迟上界约一个 tick，对业务语义足够。
	DefaultInterval = 2 * time.Second
	// DefaultBatchSize 是单轮最多投递的消息数，防单轮拖太久；
	// 积压大时每 tick 消化一批，逐步排空。
	DefaultBatchSize = 20
	// deliverTimeout 是单次投递调用的预算。为什么必须显式给：
	// relay 的 ctx 是进程级的（main 的 signal ctx），没有任何 deadline，
	// 而 product"半死不活"（黑洞 IP、对端卡死不回包）时 gRPC 调用会
	// 永远阻塞——后果不只是这条消息送不出：整个 batch 卡在它后面，
	// session 的行锁一直被持有，其他副本的 relay 也被堵死（2026-09-12
	// 在搜索路径实测到同类事故，见 search_repository.go 的超时注释）。
	// 3s 的依据：容器网络内健康 product 的响应是毫秒级，3s 已极度宽容；
	// 超时按普通失败走 MarkFailed 退避，由"下一轮再来"自愈。
	deliverTimeout = 3 * time.Second
)

// Relay 是投递循环。零依赖全局状态：tracer/meter 从 OTel 全局取
// （未初始化时是 noop，单测无感），repo 和 product client 由调用方注入。
type Relay struct {
	outbox    repository.OutboxRepository
	products  repository.ProductClient
	log       *zap.Logger
	tracer    trace.Tracer
	interval  time.Duration
	batchSize int

	delivered metric.Int64Counter
	dead      metric.Int64Counter
	pending   metric.Int64Gauge
}

// NewRelay 组装 relay。interval/batchSize 传 0 用默认值。
func NewRelay(outbox repository.OutboxRepository, products repository.ProductClient, log *zap.Logger, interval time.Duration, batchSize int) *Relay {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}

	meter := otel.Meter("go-ecom-admin/order/outbox")
	// 指标初始化失败只在"同名不同类型重复注册"这种编程错误时发生，
	// 拿不到就是 nil——Add/Record 前判空，指标是运维增强，不挡业务。
	delivered, _ := meter.Int64Counter("outbox_delivered_total")
	dead, _ := meter.Int64Counter("outbox_dead_total")
	pending, _ := meter.Int64Gauge("outbox_pending")

	return &Relay{
		outbox:    outbox,
		products:  products,
		log:       log,
		tracer:    otel.GetTracerProvider().Tracer("go-ecom-admin/order/outbox"),
		interval:  interval,
		batchSize: batchSize,
		delivered: delivered,
		dead:      dead,
		pending:   pending,
	}
}

// Run 阻塞运行投递循环，ctx 取消时优雅退出（在途一轮投递完成后返回）。
// 由 main.go 的 go relay.Run(ctx) 启动，生命周期与服务进程一致。
func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.log.Info("outbox relay stopped")
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

// tick 是一轮投递：抢一批 → 逐条投递 → 收尾提交 → 上报积压指标。
// 任何单条失败都不影响其他消息（每条独立 Mark），tick 级错误只记日志——
// relay 是长周期后台循环，靠"下一轮再来"自愈，不能 panic 带崩服务。
func (r *Relay) tick(ctx context.Context) {
	ctx, span := r.tracer.Start(ctx, "outbox-relay.tick")
	defer span.End()

	// 先上报积压再抢消息（此时无事务持有连接）：PENDING 总量含退避
	// 等待中的，是"这个集群还有多少回没送达"的真实读数。
	if n, err := r.outbox.CountPending(ctx); err == nil && r.pending != nil {
		r.pending.Record(ctx, n)
	}

	sess, err := r.outbox.ClaimPending(ctx, r.batchSize)
	if err != nil {
		r.log.Error("outbox claim failed", zap.Error(err))
		span.SetStatus(codes.Error, "claim failed")
		return
	}
	if sess == nil {
		return // 本轮无可投递，最常见的路径
	}

	// 投递完成后无论成败都要 Close（提交标记）；漏掉 Close 会让事务
	// 悬着直到连接回收——行锁一直被持有，阻塞其他副本。
	defer func() {
		if err := sess.Close(); err != nil {
			r.log.Error("outbox session close failed", zap.Error(err))
		}
	}()

	for i := range sess.Messages {
		r.deliver(ctx, sess, &sess.Messages[i])
	}
}

// deliver 投递单条消息。成功 MarkSent；失败 MarkFailed（退避/死信在
// session 里算好，dead=true 时补 ERROR 日志）。
func (r *Relay) deliver(ctx context.Context, sess *repository.OutboxSession, m *model.OutboxMessage) {
	ctx, span := r.tracer.Start(ctx, "outbox-relay.deliver",
		trace.WithAttributes(
			attribute.String("outbox.message_id", m.MessageID),
			attribute.String("outbox.type", m.Type),
			attribute.Int("outbox.retry_count", m.RetryCount),
		))
	defer span.End()

	var p model.StockRestorePayload
	if err := json.Unmarshal([]byte(m.Payload), &p); err != nil {
		// 消息体损坏是永久性失败：重试一万次也不会好，
		// 直接死信（MarkDead 跳过退避），别浪费重试窗口。
		r.log.Error("OUTBOX DEAD: undecodable payload",
			zap.Uint64("outbox_id", m.ID), zap.String("message_id", m.MessageID), zap.Error(err))
		span.SetStatus(codes.Error, "undecodable payload")
		if err := sess.MarkDead(ctx, m.ID); err != nil {
			r.log.Error("mark dead after undecodable payload", zap.Error(err))
		}
		if r.dead != nil {
			r.dead.Add(ctx, 1)
		}
		return
	}

	// 系统身份：relay 是后台进程，没有真实用户，按约定注入 user_id=0。
	// product 侧只有 RestoreStock（有 message_id 幂等背书）放行 0。
	// 调用预算 deliverTimeout 的理由见常量注释——这是进程级 ctx 上
	// 唯一的失败边界。
	callCtx, cancel := context.WithTimeout(ctx, deliverTimeout)
	err := r.products.RestoreStock(identity.InjectOutgoingSystem(callCtx), p.ProductID, p.Quantity, m.MessageID)
	cancel()
	if err == nil {
		if err := sess.MarkSent(ctx, m.ID); err != nil {
			r.log.Error("mark sent failed", zap.Uint64("outbox_id", m.ID), zap.Error(err))
			span.SetStatus(codes.Error, "mark sent failed")
			return
		}
		if r.delivered != nil {
			r.delivered.Add(ctx, 1)
		}
		span.SetAttributes(attribute.Bool("outbox.delivered", true))
		return
	}

	dead, markErr := sess.MarkFailed(ctx, m.ID, err)
	if markErr != nil {
		r.log.Error("mark failed errored", zap.Uint64("outbox_id", m.ID), zap.Error(markErr))
		span.SetStatus(codes.Error, "mark failed errored")
		return
	}
	span.SetStatus(codes.Error, err.Error())

	if dead {
		// 死信：重试耗尽。到这里说明 product 侧持续故障（如商品已删除），
		// 自动投递放弃，换人工/重放工具处置（4b）。
		r.log.Error("OUTBOX DEAD: delivery exhausted, manual action required",
			zap.Uint64("outbox_id", m.ID),
			zap.String("message_id", m.MessageID),
			zap.Uint64("product_id", p.ProductID),
			zap.Int32("quantity", p.Quantity),
			zap.Uint64("order_id", p.OrderID),
			zap.Int("retries", m.RetryCount+1),
			zap.Error(err),
		)
		if r.dead != nil {
			r.dead.Add(ctx, 1)
		}
		return
	}
	r.log.Warn("outbox delivery failed, will retry with backoff",
		zap.Uint64("outbox_id", m.ID),
		zap.String("message_id", m.MessageID),
		zap.Int("retry_count", m.RetryCount+1),
		zap.Error(err),
	)
}
