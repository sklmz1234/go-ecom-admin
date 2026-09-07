// metrics.go：指标支柱的初始化，与 tracer.go 完全对称——同一个 OTel SDK，
// trace 走 push（OTLP 推给 Jaeger），metrics 走 pull（Prometheus 来 /metrics 拉）。
//
// 为什么指标也选 OTel API 而不是 Prometheus 官方的 client_golang：
// otelgrpc stats handler 和 gorm otel 插件的指标埋点都是调 OTel metric API
// 写的——全局 MeterProvider 一设，gRPC 每方法调用数/耗时、SQL 耗时这些
// 指标零成本白拿；若选 client_golang，这两块就得自己重新埋一遍点。
// 一套 SDK 打通三支柱，语义统一，未来想换 push 模式（OTLP → Prometheus
// native OTLP ingestion）也只改 exporter 不动埋点。
package telemetry

import (
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricsPort 是两个 gRPC 服务暴露 /metrics 的运维端口。9464 是 Prometheus
// 官方端口分配表里留给 OTel 的端口，用它属于业界惯例，运维看到端口就知道
// 是什么。业务端口（9001/9002）和运维端口分离：K8s 下可以用 NetworkPolicy
// 只对内网放开 9464，指标接口不随业务流量暴露。
const MetricsPort = 9464

// SetupMetrics 初始化全局 MeterProvider（Prometheus pull 模式），返回的
// http.Handler 就是要挂到 /metrics 端点的 handler——exporter 把自己注册成
// client_golang 默认 registry 的一个 collector，所以端点直接用官方
// promhttp.Handler() 暴露（client_golang 是 exporter 的传递依赖，
// 不算为指标新引一套体系）。
//
// 与 Setup（trace）的容错语义不同：这里 New 只组装内存对象、不碰网络，
// 唯一现实的失败是 MeterProvider 已存在之类的编程错误，所以调用方
// Fatal 是合理的（配置/代码错误要尽早炸出来）。
//
// 返回的 provider 需要在进程退出前 Shutdown（停掉定期收集协程）；
// pull 模式没有"未发出去的缓冲数据"，所以 Shutdown 不像 trace 那样
// 关系数据丢失，更多是干净退出的习惯。
func SetupMetrics() (*sdkmetric.MeterProvider, http.Handler, error) {
	exp, err := prometheus.New()
	if err != nil {
		return nil, nil, err
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exp))
	// 设为全局：otelgrpc / gorm 插件 / 网关 HTTP 中间件都从全局取 meter，
	// 与 SetTracerProvider 同一个套路。
	otel.SetMeterProvider(mp)

	// Go runtime 指标（goroutine 数、GC、堆内存）：纯 Go 进程的自我体检，
	// 一次启动终身免费。默认每 15s 读一次 runtime.ReadMemStats，
	// 频率够用且不会给 GC 添压力。
	if err := runtime.Start(); err != nil {
		return nil, nil, err
	}

	return mp, promhttp.Handler(), nil
}

// NewMetricsServer 组装只挂 /metrics 一个端点的运维 HTTP 服务，两个 gRPC
// 服务的 main 共用（避免各写一遍 http.Server 样板）。调用方负责 go
// ListenAndServe() 并在退出时 Shutdown。
func NewMetricsServer(handler http.Handler) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", handler)
	return &http.Server{
		Addr:              fmt.Sprintf(":%d", MetricsPort),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}
