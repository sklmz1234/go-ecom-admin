// Package identity 统一处理"调用方是谁"在 gRPC 调用链上的传递。
//
// 为什么需要它：user_id 的 metadata key 曾经在网关（注入侧）和
// product-service（读取侧）各写了一遍字符串字面量，两处硬编码已经在
// 漂移边缘——任何一边改了 key 而另一边没改，写路径会全部 401 且极难排查。
// 阶段 3 出现第三个使用方（order-service 调 product-service 时要把
// incoming 的身份显式接力到 outgoing），三处共用把这个隐患彻底收掉。
//
// 边界：api-gateway 是认证边界，JWT 只在那里验签；下游服务只认 metadata
// 里的 user_id。身份用于追责（审计日志），归属校验（授权）是另一码事，
// 由各服务自己决定哪些方法需要（如 product 的 Update/Delete）。
package identity

import (
	"context"
	"strconv"

	"google.golang.org/grpc/metadata"

	apperrors "go-ecom-admin/pkg/errors"
)

// MetadataKeyUserID 是 user_id 在 gRPC metadata 里的 key（小写下划线风格，
// gRPC metadata key 不区分大小写但统一规范成这种形式）。
const MetadataKeyUserID = "user_id"

// InjectOutgoing 把"当前登录用户是谁"附加到 outgoing context 上，随 gRPC
// 调用一起传给下游服务。两个使用场景：
//   - 网关验完 JWT 后注入（认证边界的职责）；
//   - 服务间调用时接力——metadata 不会自动从 incoming 传到 outgoing，
//     order-service 调 product-service 前必须显式再注入一次。
func InjectOutgoing(ctx context.Context, userID uint64) context.Context {
	return metadata.AppendToOutgoingContext(ctx, MetadataKeyUserID, strconv.FormatUint(userID, 10))
}

// FromIncoming 从 incoming metadata 取出调用方 user_id。缺失或格式非法都按
// 未认证处理——对服务端来说，一个"自称带身份但身份读不出来"的请求和一个
// "没有身份"的请求没有区别，都不能放行到写路径。
func FromIncoming(ctx context.Context) (uint64, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return 0, apperrors.Unauthorized("missing caller identity", nil)
	}

	vals := md.Get(MetadataKeyUserID)
	if len(vals) == 0 {
		return 0, apperrors.Unauthorized("missing caller identity", nil)
	}

	uid, err := strconv.ParseUint(vals[0], 10, 64)
	if err != nil || uid == 0 {
		return 0, apperrors.Unauthorized("invalid caller identity", err)
	}
	return uid, nil
}
