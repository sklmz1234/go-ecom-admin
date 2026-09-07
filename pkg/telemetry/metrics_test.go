package telemetry

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

// SetupMetrics 的核心契约：初始化之后，/metrics 端点必须能拉到
// 业务自己记录的指标。pull 模式最怕"代码埋了点、端点却是空的"——
// 这条测试把"埋点 → 暴露"的完整链路钉住。
func TestSetupMetrics_RecordedMetricShowsUpInScrape(t *testing.T) {
	mp, handler, err := SetupMetrics()
	if err != nil {
		t.Fatalf("SetupMetrics: %v", err)
	}
	defer func() { _ = mp.Shutdown(t.Context()) }()

	// 业务侧视角：从全局 provider 拿 meter 埋点（和 httpmetrics 中间件
	// 的用法一致），不直接碰 mp——测的就是"全局设置生效了"。
	counter, err := otel.Meter("test").Int64Counter("test.setupmetrics.calls")
	if err != nil {
		t.Fatalf("create counter: %v", err)
	}
	counter.Add(t.Context(), 42)

	body := scrape(t, handler)
	// Prometheus exporter 会把指标名规范化（点 → 下划线），且计数器带
	// otel_scope 标签，所以分别断言名字和值而不是整行。
	if !strings.Contains(body, "test_setupmetrics_calls_total{") || !strings.Contains(body, "} 42\n") {
		t.Fatalf("scraped output should contain the recorded counter = 42, got:\n%s", body)
	}
}

// Go runtime 指标是"一次启动终身免费"的那类，确认它们真的出现在
// 暴露内容里——goroutine 数指标是每个 Go 服务面板的基本盘。
func TestSetupMetrics_RuntimeMetricsExposed(t *testing.T) {
	mp, handler, err := SetupMetrics()
	if err != nil {
		t.Fatalf("SetupMetrics: %v", err)
	}
	defer func() { _ = mp.Shutdown(t.Context()) }()

	// runtime instrumentation 的首次收集需要时间注册/读数，
	// 拉两次并留一点余量：第一次触发，第二次拿数据。
	scrape(t, handler)
	time.Sleep(50 * time.Millisecond)
	body := scrape(t, handler)

	if !strings.Contains(body, "go_goroutine_count") {
		t.Fatalf("scraped output should contain runtime metrics (go_goroutine_count), got:\n%s", body)
	}
}

// NewMetricsServer 只暴露 /metrics：运维端口上不该有业务路由，
// 其他路径一律 404——这条语义防止以后有人图省事往运维端口上挂业务接口。
func TestNewMetricsServer_OnlyMetricsPath(t *testing.T) {
	_, handler, err := SetupMetrics()
	if err != nil {
		t.Fatalf("SetupMetrics: %v", err)
	}
	srv := NewMetricsServer(handler)

	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("/metrics status = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/products", nil))
	if rec.Code != 404 {
		t.Fatalf("non-metrics path status = %d, want 404", rec.Code)
	}
}

func scrape(t *testing.T, handler http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("scrape status = %d, want 200", rec.Code)
	}
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read scrape body: %v", err)
	}
	return string(body)
}
