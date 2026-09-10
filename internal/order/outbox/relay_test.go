// relay 单元测试：真实 sqlite repo（状态机是真的）+ mockery 的
// MockProductClient（投递成败可控）。
//
// 不 mock outbox repo 的理由：relay 测试的重点是"投递结果如何反映到
// 消息状态"（SENT/退避/DEAD），中间隔一层 mock 只能把被测逻辑复述一遍。
package outbox

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"go-ecom-admin/internal/order/model"
	"go-ecom-admin/internal/order/repository"
	"go-ecom-admin/internal/order/repository/mocks"
	"go-ecom-admin/pkg/identity"
)

func newRelay(t *testing.T) (*Relay, *gorm.DB, *mocks.MockProductClient) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	// sqlite :memory: 连接池锁单连接的理由见 order repository 测试注释。
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.OutboxMessage{}))

	products := mocks.NewMockProductClient(t)
	relay := NewRelay(repository.NewOutboxRepository(db), products, zaptest.NewLogger(t), time.Hour, 10)
	return relay, db, products
}

func insertPending(t *testing.T, db *gorm.DB, messageID string) *model.OutboxMessage {
	t.Helper()
	payload, err := json.Marshal(model.StockRestorePayload{ProductID: 7, Quantity: 3, OrderID: 1001})
	require.NoError(t, err)
	m := &model.OutboxMessage{
		MessageID:   messageID,
		Type:        model.OutboxTypeStockRestore,
		Payload:     string(payload),
		Status:      model.OutboxStatusPending,
		NextRetryAt: time.Now(),
	}
	require.NoError(t, db.Create(m).Error)
	return m
}

func loadMessage(t *testing.T, db *gorm.DB, id uint64) model.OutboxMessage {
	t.Helper()
	var m model.OutboxMessage
	require.NoError(t, db.First(&m, id).Error)
	return m
}

// 投递成功：消息置 SENT；调用 product 时带系统身份（user_id=0）和幂等键。
func TestRelay_DeliverSuccess_MarksSent(t *testing.T) {
	relay, db, products := newRelay(t)
	ctx := context.Background()
	m := insertPending(t, db, "msg-1")

	products.EXPECT().RestoreStock(mock.Anything, uint64(7), int32(3), "msg-1").
		Run(func(ctx context.Context, productID uint64, quantity int32, messageID string) {
			// relay 是后台进程：身份必须是系统约定 user_id=0，
			// 而不是碰巧没人检查——product 侧只对 RestoreStock 放行它。
			md, ok := metadata.FromOutgoingContext(ctx)
			require.True(t, ok, "投递 ctx 必须带身份 metadata")
			assert.Equal(t, []string{"0"}, md[identity.MetadataKeyUserID])
		}).Return(nil)

	relay.tick(ctx)

	got := loadMessage(t, db, m.ID)
	assert.Equal(t, model.OutboxStatusSent, got.Status)
	require.NotNil(t, got.SentAt)
}

// 投递失败：retry_count+1、指数退避推远 next_retry_at；退避窗口内
// 下一轮 tick 不会再碰它。
func TestRelay_DeliverFailure_BackoffAndDefer(t *testing.T) {
	relay, db, products := newRelay(t)
	ctx := context.Background()
	m := insertPending(t, db, "msg-1")

	products.EXPECT().RestoreStock(mock.Anything, uint64(7), int32(3), "msg-1").
		Return(status.Error(codes.Unavailable, "product-service down"))

	before := time.Now()
	relay.tick(ctx)

	got := loadMessage(t, db, m.ID)
	assert.Equal(t, model.OutboxStatusPending, got.Status)
	assert.Equal(t, 1, got.RetryCount)
	assert.WithinDuration(t, before.Add(2*time.Second), got.NextRetryAt, 2*time.Second)

	// 退避未到期：第二轮 tick 空转（mock 零期望=不会再调用 product）。
	relay.tick(ctx)
}

// 连续失败到上限：DEAD 死信 + ERROR 日志（zaptest 捕获），之后不再投递。
func TestRelay_DeadAfterExhaustedRetries(t *testing.T) {
	relay, db, products := newRelay(t)
	ctx := context.Background()
	m := insertPending(t, db, "msg-1")

	down := status.Error(codes.Unavailable, "product-service down")
	// 第 1~10 次投递全部失败（每轮 tick 后手动把退避拨到期，模拟时间流逝）。
	for i := 1; i <= repository.MaxOutboxRetry; i++ {
		products.EXPECT().RestoreStock(mock.Anything, uint64(7), int32(3), "msg-1").Return(down)
		relay.tick(ctx)

		require.NoError(t, db.Model(&model.OutboxMessage{}).
			Where("id = ?", m.ID).
			Update("next_retry_at", time.Now().Add(-time.Second)).Error)
	}

	got := loadMessage(t, db, m.ID)
	assert.Equal(t, model.OutboxStatusDead, got.Status, "重试耗尽必须死信")

	// 死信后 tick 空转——mock 不再注册任何期望，被调用即测试失败。
	relay.tick(ctx)
}

// 消息体损坏：不重试直接死信（重试一万次也不会好）。
func TestRelay_UndecodablePayload_GoesDead(t *testing.T) {
	relay, db, _ := newRelay(t)
	ctx := context.Background()

	m := insertPending(t, db, "msg-bad")
	require.NoError(t, db.Model(&model.OutboxMessage{}).
		Where("id = ?", m.ID).
		Update("payload", "{not json").Error)

	relay.tick(ctx)

	got := loadMessage(t, db, m.ID)
	assert.Equal(t, model.OutboxStatusDead, got.Status)
	assert.Zero(t, got.RetryCount, "直接死信不计退避次数")
}
