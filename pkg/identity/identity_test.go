// pkg/identity 的往返测试：InjectOutgoing 注入的身份，下游用 FromIncoming
// 必须能原样取出——这是跨服务身份链路的"宪法级"行为，key 漂移会在这里立刻暴露。
package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	apperrors "go-ecom-admin/pkg/errors"

	"go-ecom-admin/pkg/identity"
)

// simulateHop 模拟一跳 gRPC 调用：client 侧 InjectOutgoing 写 outgoing metadata，
// server 侧看到的是 incoming metadata——gRPC 在传输层做的正是这件事，
// 测试里手动转换，不依赖真实网络。
func simulateHop(ctx context.Context) context.Context {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return context.Background()
	}
	return metadata.NewIncomingContext(context.Background(), md)
}

func TestInjectThenExtract_RoundTrip(t *testing.T) {
	ctx := identity.InjectOutgoing(context.Background(), 42)

	uid, err := identity.FromIncoming(simulateHop(ctx))

	require.NoError(t, err)
	assert.Equal(t, uint64(42), uid)
}

func TestFromIncoming_Errors(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{"完全没有metadata", context.Background()},
		{"metadata里没有user_id", metadata.NewIncomingContext(context.Background(), metadata.Pairs("other", "x"))},
		{"user_id不是数字", metadata.NewIncomingContext(context.Background(), metadata.Pairs(identity.MetadataKeyUserID, "abc"))},
		{"user_id为0", metadata.NewIncomingContext(context.Background(), metadata.Pairs(identity.MetadataKeyUserID, "0"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := identity.FromIncoming(tt.ctx)

			require.Error(t, err)
			var appErr *apperrors.AppError
			require.True(t, errors.As(err, &appErr), "应该返回 *AppError，实际是 %T", err)
			assert.Equal(t, apperrors.CodeUnauthorized, appErr.Code)
		})
	}
}
