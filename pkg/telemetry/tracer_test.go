package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestTraceIDFromContext_NoSpanReturnsEmpty(t *testing.T) {
	if got := TraceIDFromContext(context.Background()); got != "" {
		t.Fatalf("expected empty trace_id without span, got %q", got)
	}
}

func TestTraceIDFromContext_WithSpanReturnsHexID(t *testing.T) {
	tid, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("parse trace id: %v", err)
	}
	sid, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("parse span id: %v", err)
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
		Remote:     true, // 模拟从上游传播进来的 span context
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	if got, want := TraceIDFromContext(ctx), "4bf92f3577b34da6a3ce929d0e0e4736"; got != want {
		t.Fatalf("trace_id = %q, want %q", got, want)
	}
}

// Setup 对不可达的 endpoint 也必须成功：exporter 是惰性连接（真正的导出
// 由后台协程异步做），Jaeger 没起来不能挡住业务服务启动——观测系统的
// 故障不能传染给被观测的系统，这条语义靠测试钉住。
func TestSetup_UnreachableEndpointStillSucceeds(t *testing.T) {
	tp, err := Setup(context.Background(), Config{
		ServiceName:  "test-service",
		OTLPEndpoint: "unreachable.invalid:4317",
		SampleRatio:  1.0,
	})
	if err != nil {
		t.Fatalf("Setup should not fail on unreachable endpoint: %v", err)
	}
	// Shutdown 空队列不应阻塞报错。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	// 超时 0 的 ctx：BSP 队列为空时直接返回；万一尝试导出也只会因超时放弃，
	// 这正是"导出失败不影响进程退出"的体现。
	_ = tp.Shutdown(shutdownCtx)
}
