// userIDFromContext 从 gRPC incoming metadata 里取出调用方身份。
//
// 这就是零信任原则的落地点：product-service 不信任任何上游（包括 api-gateway）
// 关于"这是谁的请求"的口头声明之外的任何东西——但它要求上游必须声明身份。
// 没有身份 = 未认证，直接拒绝，不允许出现"无主商品"这种模糊状态：
// 归属模型里每一行数据都必须有确定的责任人，否则 Update/Delete 的
// 归属校验会被"owner_id=0 的遗留数据该怎么处理"这类问题腐蚀掉。
//
// metadata 是 gRPC 世界的 HTTP header：key 会被强制转小写，value 是 []string。
// user_id 由 api-gateway 在验完 JWT 后通过 metadata.AppendToOutgoingContext 注入。
package service

import (
	"context"
	"strconv"

	"google.golang.org/grpc/metadata"

	apperrors "go-ecom-admin/pkg/errors"
)

// metadataKeyUserID 与 api-gateway 注入时使用的 key 保持一致（小写下划线风格，
// gRPC metadata key 不区分大小写但统一规范成这种形式，避免两边写法漂移）。
const metadataKeyUserID = "user_id"

// userIDFromContext 取出调用方 user_id。缺失或格式非法都按未认证处理——
// 对服务端来说，一个"自称带身份但身份读不出来"的请求和一个"没有身份"的请求
// 没有区别，都不能放行到写路径。
func userIDFromContext(ctx context.Context) (uint64, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return 0, apperrors.Unauthorized("missing caller identity", nil)
	}

	vals := md.Get(metadataKeyUserID)
	if len(vals) == 0 {
		return 0, apperrors.Unauthorized("missing caller identity", nil)
	}

	uid, err := strconv.ParseUint(vals[0], 10, 64)
	if err != nil || uid == 0 {
		return 0, apperrors.Unauthorized("invalid caller identity", err)
	}
	return uid, nil
}
