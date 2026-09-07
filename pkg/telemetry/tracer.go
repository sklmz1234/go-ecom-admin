// Package telemetry 初始化 OpenTelemetry 链路追踪，三个服务共用。
//
// 数据流：业务代码产生 span → BatchSpanProcessor 异步攒批 → OTLP gRPC
// exporter 推给 Jaeger（compose 网络内的 jaeger:4317）→ Jaeger UI 聚合展示。
//
// 关键设计决策（面试三问的自答）：
//
//  1. 不用追踪会坏什么？跨服务排障只能逐个 docker logs / kubectl logs 翻，
//     "internal error 到底挂在哪一跳"要人肉二分。有 trace 后一次请求的
//     完整瀑布图（HTTP → gRPC → SQL）直接定位最慢/出错的那一跳。
//
//  2. 用它的代价？每次请求多几个 span 的产生/序列化/网络开销（微秒级，
//     对业务延迟的影响 <1%）；Jaeger 多一个容器；采样率是成本旋钮。
//
//  3. 什么场景应该去掉？纯本地裸跑调试单服务、或压测基准时（排除观测
//     开销对数据的污染）——所以有 telemetry.enabled 开关，关掉即零开销。
//
// 为什么用 contrib 库（otelgin/otelgrpc/gorm plugin）而不手写埋点：
// span 生命周期管理、W3C traceparent 的注入/提取、错误状态标记这些细节
// 官方库都经过大规模生产验证；手写一遍适合学习，但生产代码的正确姿势是
// "把精力花在业务和采样策略上，埋点交给标准库"。
package telemetry

import (
	"context"
	"runtime/debug"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Config 描述一个进程的追踪身份与导出目标。ServiceName 由各 main.go
// 显式传入（"api-gateway" / "user-service" / "product-service"），
// 不从 yaml 读——见 pkg/config TelemetryConfig 的注释。
type Config struct {
	ServiceName  string
	OTLPEndpoint string
	SampleRatio  float64
}

// Setup 初始化全局 TracerProvider 和 propagator，返回 provider 供
// 调用方在进程退出前 Shutdown（把批处理里还没发出去的 span 冲洗掉——
// 不做这步，进程最后 ~5 秒的 trace 会丢）。
//
// 容错语义：Jaeger 暂时不可达时 Setup 也不会失败（exporter 是惰性连接），
// 之后 BatchSpanProcessor 导出失败只会记日志丢批，绝不阻塞业务请求——
// 观测系统的故障不能传染给被观测的系统。
func Setup(ctx context.Context, cfg Config) (*sdktrace.TracerProvider, error) {
	// OTLP gRPC 是 OTel 官方标准协议（4317 端口）：换后端（Tempo/Datadog/
	// 阿云 ARMS）只改 endpoint，代码零改动——这就是当初选它而非 Jaeger
	// 专用 thrift 协议的原因。
	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
		// 容器网络内明文传输；生产上 collector 通常在同一个内网，TLS 由
		// 基础设施层（service mesh / 网络策略）负责，应用不强求。
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	// resource 是随每个 span 附带的进程身份标签，Jaeger 里按 service 名
	// 分组/检索就靠它。
	res, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			// semconv 版本号变量：让 span 里的 schema 语义可追溯。
			semconv.ServiceVersion(version()),
		),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		// BatchSpanProcessor：业务 goroutine 只把 span 塞进内存队列就返回，
		// 真正的序列化/网络发送由后台协程批量做——同步导出会拖慢每个请求。
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		// ParentBased(TraceIDRatioBased(r))：头部采样。入口按 trace_id
		// 哈希以 r 的概率决定采样，子 span 跟随父决策——保证同一条链路
		// 要么全采、要么全不采，不会出现"网关有、下游没有"的断链。
		// r=1.0 全采（本地默认），生产常见 0.01~0.1。
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	)

	// 设置为全局 provider：otelgin/otelgrpc/gorm 插件都从全局取，
	// 各自的中间件/拦截器代码不用传参。
	otel.SetTracerProvider(tp)
	// W3C TraceContext（traceparent header）：跨进程传播的事实标准，
	// gin↔gRPC↔gRPC 之间就靠它把 trace_id 串起来。
	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	return tp, nil
}

// TraceIDFromContext 提取当前 span 的 trace_id，供日志中间件把 trace_id
// 打进访问日志——日志/指标/追踪三支柱靠这个 ID 互相跳转（Jaeger 里搜
// trace_id 能直接定位这条日志对应的完整链路）。无 span 或未采样返回空串。
func TraceIDFromContext(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if sc.HasTraceID() {
		return sc.TraceID().String()
	}
	return ""
}

// version 从构建信息里取 git commit（go build 默认注入 vcs.revision）。
// 取不到（如 go run）就退化为 "dev"，只是个展示字段，不值得为它引依赖。
func version() string {
	if bi, ok := debugBuildInfo(); ok && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

func debugBuildInfo() (*debug.BuildInfo, bool) {
	return debug.ReadBuildInfo()
}
