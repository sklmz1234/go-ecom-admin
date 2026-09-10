package repository

import (
	"testing"
	"time"

	"github.com/sony/gobreaker"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// newTestBreaker 用和生产相同的阈值（连续 5 次失败打开），但把 open 时长
// 缩到 50ms，让 half-open 恢复路径可以在单测里跑到。
func newTestBreaker() *gobreaker.CircuitBreaker {
	return gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "test",
		MaxRequests: 3,
		Interval:    10 * time.Second,
		Timeout:     50 * time.Millisecond,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 5
		},
	})
}

func infraErr() error     { return status.Error(codes.Unavailable, "connection refused") }
func businessErr() error  { return status.Error(codes.FailedPrecondition, "库存不足") }
func notFoundErr() error  { return status.Error(codes.NotFound, "product not found") }

// 连续基础设施错误达到阈值后熔断打开：后续调用不再执行 fn（fail fast），
// 且错误被翻译成 Unavailable——网关会把它映射成 503。
func TestCallWithBreaker_OpensAfterConsecutiveInfraFailures(t *testing.T) {
	cb := newTestBreaker()
	calls := 0
	fn := func() (int, error) {
		calls++
		return 0, infraErr()
	}

	for i := 0; i < 5; i++ {
		if _, err := callWithBreaker(cb, fn); err == nil {
			t.Fatalf("call %d: expected infra error, got nil", i)
		}
	}
	if cb.State() != gobreaker.StateOpen {
		t.Fatalf("expected breaker open after 5 infra failures, got %v", cb.State())
	}

	callsBefore := calls
	_, err := callWithBreaker(cb, fn)
	if err == nil {
		t.Fatal("expected error while breaker open")
	}
	if st, _ := status.FromError(err); st.Code() != codes.Unavailable {
		t.Fatalf("expected Unavailable when breaker open, got %v", err)
	}
	if calls != callsBefore {
		t.Fatal("fn must not be invoked while breaker is open (fail fast)")
	}
}

// 业务错误（库存不足、查无此人……）不计入熔断失败——这是熔断器最经典的坑：
// 秒杀流量下"库存不足"占多数时，业务错误若计数会把熔断器刷开，
// 健康的服务被自己限死。
func TestCallWithBreaker_BusinessErrorsDoNotTrip(t *testing.T) {
	cb := newTestBreaker()
	for i := 0; i < 20; i++ {
		_, err := callWithBreaker(cb, func() (int, error) { return 0, businessErr() })
		if err == nil {
			t.Fatal("expected business error to pass through")
		}
		if st, _ := status.FromError(err); st.Code() != codes.FailedPrecondition {
			t.Fatalf("business error must pass through unchanged, got %v", err)
		}
	}
	if cb.State() != gobreaker.StateClosed {
		t.Fatalf("business errors must not trip the breaker, state=%v", cb.State())
	}
}

// 成功调用会重置连续失败计数：4 次失败 + 1 次成功 + 4 次失败不熔断。
func TestCallWithBreaker_SuccessResetsConsecutiveCount(t *testing.T) {
	cb := newTestBreaker()
	fail := func() (int, error) { return 0, infraErr() }
	ok := func() (int, error) { return 42, nil }

	for i := 0; i < 4; i++ {
		callWithBreaker(cb, fail)
	}
	if _, err := callWithBreaker(cb, ok); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := 0; i < 4; i++ {
		callWithBreaker(cb, fail)
	}
	if cb.State() != gobreaker.StateClosed {
		t.Fatalf("success should reset consecutive failures, state=%v", cb.State())
	}
}

// 打开后经 Timeout 进入 half-open，试探请求成功则自动闭合恢复。
func TestCallWithBreaker_HalfOpenRecovery(t *testing.T) {
	cb := newTestBreaker()
	healthy := false
	fn := func() (int, error) {
		if !healthy {
			return 0, infraErr()
		}
		return 1, nil
	}

	for i := 0; i < 5; i++ {
		callWithBreaker(cb, fn)
	}
	if cb.State() != gobreaker.StateOpen {
		t.Fatalf("expected open, got %v", cb.State())
	}

	healthy = true
	time.Sleep(60 * time.Millisecond) // 等 open → half-open

	// half-open 放 MaxRequests=3 个试探，全成功则闭合。
	for i := 0; i < 3; i++ {
		if _, err := callWithBreaker(cb, fn); err != nil {
			t.Fatalf("probe %d should succeed: %v", i, err)
		}
	}
	if cb.State() != gobreaker.StateClosed {
		t.Fatalf("expected closed after successful probes, got %v", cb.State())
	}
}

// isInfraError 分类表：只有"下游病了"的信号才计入熔断。
func TestIsInfraError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"Unavailable 计入", infraErr(), true},
		{"DeadlineExceeded 计入", status.Error(codes.DeadlineExceeded, "timeout"), true},
		{"Internal 计入", status.Error(codes.Internal, "panic"), true},
		{"FailedPrecondition 不计", businessErr(), false},
		{"NotFound 不计", notFoundErr(), false},
		{"InvalidArgument 不计", status.Error(codes.InvalidArgument, "bad input"), false},
		{"Unauthenticated 不计", status.Error(codes.Unauthenticated, "bad token"), false},
	}
	for _, tc := range cases {
		if got := isInfraError(tc.err); got != tc.want {
			t.Errorf("%s: isInfraError=%v, want %v", tc.name, got, tc.want)
		}
	}
}
