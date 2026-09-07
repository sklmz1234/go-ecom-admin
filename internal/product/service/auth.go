// Package service 的身份读取已收拢到 pkg/identity（阶段 3 提炼）。
//
// 这就是零信任原则的落地点：product-service 不信任任何上游（包括 api-gateway）
// 关于"这是谁的请求"的口头声明之外的任何东西——但它要求上游必须声明身份。
// 没有身份 = 未认证，直接拒绝，不允许出现"无主商品"这种模糊状态：
// 归属模型里每一行数据都必须有确定的责任人，否则 Update/Delete 的
// 归属校验会被"owner_id=0 的遗留数据该怎么处理"这类问题腐蚀掉。
//
// metadata 是 gRPC 世界的 HTTP header：key 会被强制转小写，value 是 []string。
// user_id 由 api-gateway 在验完 JWT 后注入；order-service 等服务间调用
// 则用同一个 pkg/identity 显式接力。
package service

import (
	"context"

	"go-ecom-admin/pkg/identity"
)

// userIDFromContext 是 identity.FromIncoming 的包内别名：保留原函数名，
// 本包内所有调用点（Create/Update/Delete/DeductStock/RestoreStock）零改动。
func userIDFromContext(ctx context.Context) (uint64, error) {
	return identity.FromIncoming(ctx)
}
