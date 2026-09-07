package middleware

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// 防标签基数爆炸是本中间件存在的核心理由：访问 /products/42 和
// /products/7 两个不同的实际路径，指标里必须归并成同一条时间序列
// （标签 http.route=/products/:id），而不是每个 id 一条。
func TestHTTPMetrics_UsesRoutePatternNotRawPath(t *testing.T) {
	reader := setupMeter(t)
	engine := newTestEngine()

	for _, id := range []string{"42", "7"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/products/"+id, nil)
		engine.ServeHTTP(rec, req)
	}

	routes := collectRoutes(t, reader, "http.server.request.count")
	if len(routes) != 1 || routes[0] != "/products/:id" {
		t.Fatalf("expected a single series with route pattern /products/:id, got %v", routes)
	}
}

// 404（未匹配路由）也必须归进固定标签桶——扫描器乱打的 URL 同样是
// 高基数来源，不能让原始路径漏进标签。
func TestHTTPMetrics_UnmatchedRouteFallsIntoFixedBucket(t *testing.T) {
	reader := setupMeter(t)
	engine := newTestEngine()

	for _, p := range []string{"/aaa", "/bbb/1"} {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
	}

	routes := collectRoutes(t, reader, "http.server.request.count")
	if len(routes) != 1 || routes[0] != "unmatched" {
		t.Fatalf("expected all unmatched paths in one fixed bucket, got %v", routes)
	}
}

// 状态码要进标签——错误率面板（5xx 占比）就靠这个维度切。
func TestHTTPMetrics_StatusCodeRecorded(t *testing.T) {
	reader := setupMeter(t)
	engine := newTestEngine()

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest("GET", "/products/42", nil))

	attrs := collectAttrs(t, reader, "http.server.request.count")
	v, ok := attrs.Value("http.response.status_code")
	if !ok || v.AsInt64() != 200 {
		t.Fatalf("expected http.response.status_code=200 in attributes, got %v", attrs.ToSlice())
	}
}

// setupMeter 装一个手动收集的 MeterProvider 为全局，返回 reader 供断言；
// 结束后把全局 provider 还原成 noop，避免污染同包其他测试。
func setupMeter(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)
	t.Cleanup(func() {
		_ = mp.Shutdown(context.Background())
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
	})
	return reader
}

func newTestEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(HTTPMetrics())
	r.GET("/products/:id", func(c *gin.Context) { c.Status(200) })
	return r
}

// collectRoutes 收集指定指标所有数据点的 http.route 标签值（去重前），
// 数据点条数本身就是"时间序列条数"的断言对象。
func collectRoutes(t *testing.T, reader *sdkmetric.ManualReader, metricName string) []string {
	t.Helper()
	var routes []string
	for _, attrs := range collectAllAttrs(t, reader, metricName) {
		if v, ok := attrs.Value("http.route"); ok {
			routes = append(routes, v.AsString())
		}
	}
	return routes
}

func collectAttrs(t *testing.T, reader *sdkmetric.ManualReader, metricName string) attribute.Set {
	t.Helper()
	all := collectAllAttrs(t, reader, metricName)
	if len(all) == 0 {
		t.Fatalf("metric %q has no data points", metricName)
	}
	return all[0]
}

func collectAllAttrs(t *testing.T, reader *sdkmetric.ManualReader, metricName string) []attribute.Set {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	var out []attribute.Set
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metricName {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("metric %q is not an int64 sum, got %T", metricName, m.Data)
			}
			for _, dp := range sum.DataPoints {
				out = append(out, dp.Attributes)
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("metric %q not found in collected data", metricName)
	}
	return out
}
