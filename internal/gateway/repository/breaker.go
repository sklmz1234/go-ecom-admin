// Package repository 的熔断器（阶段 4B）：包在每个下游 gRPC 调用外面，
// 解决"慢失败"问题——超时（client.go 的 defaultCallTimeout）保证单次调用
// 最坏 3s 返回，但下游挂死时每个请求都白等 3s，并发一高网关协程照样堆积。
// 熔断器在"连续失败"超过阈值后打开，之后的请求毫秒级快速失败（fail fast），
// 把资源留给还健康的下游；过一阵放几个试探请求（half-open），
// 下游恢复则自动闭合。
//
// 状态机（gobreaker 实现，和 Hystrix 同一模型）：
//   closed（正常）--连续失败>=5--> open（直接拒绝）--10s--> half-open（放 3 个试探）
//   half-open --试探全成功--> closed；--任一失败--> open（重新计时）
package repository

import (
	"time"

	"github.com/sony/gobreaker"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// 熔断参数（学习项目取直觉值，注释说明调参思路）：
// - 连续 5 次基础设施失败打开：比"按错误率"直白，低流量下也确定可触发
//   （错误率模式在 QPS 个位数时分子分母都没意义，是生产高 QPS 场景的选择）；
// - open 持续 10s 后进入 half-open：比重试退避长一个量级，给下游留重启时间；
// - half-open 放 3 个试探：1 个太脆（下游刚起可能还在 warmup），
//   太多则下游没恢复时又被冲一波；
// - closed 状态每 10s 清零计数：避免"上午失败 3 次、下午失败 2 次"被累计误开。
func newBreaker(name string, log *zap.Logger) *gobreaker.CircuitBreaker {
	return gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        name,
		MaxRequests: 3,
		Interval:    10 * time.Second,
		Timeout:     10 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 5
		},
		// 状态迁移是"事故信号"：打开意味着下游出事了，闭合意味着恢复，
		// 都必须能在日志里搜到（WARN 级，本地 console 编码下肉眼可见）。
		OnStateChange: func(name string, from, to gobreaker.State) {
			log.Warn("circuit breaker state changed",
				zap.String("breaker", name),
				zap.String("from", from.String()),
				zap.String("to", to.String()))
		},
	})
}

// isInfraError 判断错误是否该计入熔断失败——熔断器最经典的坑就是
// 把业务错误算进去：秒杀时大量"库存不足"（FailedPrecondition）如果把
// 熔断器刷开，全场请求都会拿到 503，等于自己把自己限死了。
// 只有"下游病了"的信号才计数：连接不上/超时/下游内部崩溃。
func isInfraError(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		// 非 gRPC 错误（如连接层失败）视为基础设施问题，计入。
		return true
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Internal:
		return true
	default:
		return false
	}
}

// callWithBreaker 用熔断器包一次下游调用，泛型 T 是调用的返回值类型。
//
// 业务错误（NotFound/InvalidArgument/FailedPrecondition 等）原样透传给调用方，
// 但向熔断器报告"成功"——见 isInfraError 的注释。
// 熔断打开时 gobreaker 返回 ErrOpenState（或 half-open 下的 ErrTooManyRequests），
// 这里统一翻译成 gRPC Unavailable，让网关错误映射（apperrors.ToHTTPStatus）
// 走现成路径返回 503，handler/service 层完全无感。
func callWithBreaker[T any](cb *gobreaker.CircuitBreaker, fn func() (T, error)) (T, error) {
	type result struct {
		val T
		err error // 业务错误：透传但不计熔断失败
	}

	out, err := cb.Execute(func() (interface{}, error) {
		val, callErr := fn()
		switch {
		case callErr == nil:
			return result{val: val}, nil
		case isInfraError(callErr):
			return nil, callErr // 基础设施错误：计入失败，触发熔断统计
		default:
			return result{val: val, err: callErr}, nil // 业务错误：向熔断器报成功
		}
	})
	if err != nil {
		var zero T
		if err == gobreaker.ErrOpenState || err == gobreaker.ErrTooManyRequests {
			return zero, status.Error(codes.Unavailable, "service temporarily unavailable, please retry later")
		}
		return zero, err
	}
	res := out.(result)
	return res.val, res.err
}
