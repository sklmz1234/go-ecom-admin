// Command api-gateway 启动 REST API 网关：接收 HTTP 请求，转发给 user-service /
// product-service 的 gRPC 接口。
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"go-ecom-admin/pkg/config"
	"go-ecom-admin/pkg/logger"
	"go-ecom-admin/pkg/telemetry"

	"go-ecom-admin/internal/gateway/handler"
	"go-ecom-admin/internal/gateway/repository"
	"go-ecom-admin/internal/gateway/router"
	"go-ecom-admin/internal/gateway/service"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	log, err := logger.New(logger.Config{
		Level:       cfg.Log.Level,
		Encoding:    cfg.Log.Encoding,
		OutputPaths: cfg.Log.OutputPath,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "init logger: %v\n", err)
		os.Exit(1)
	}
	defer log.Sync()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 链路追踪（阶段 2D）：网关是整条链路的入口 span 产生地。初始化失败
	// 选择 Fatal——追踪地址配错属于部署错误，带着错误配置"静默降级"会让
	// 人误以为追踪正常，排障时才发现 Jaeger 上一条数据都没有。
	var tracerProvider *sdktrace.TracerProvider
	var meterProvider *sdkmetric.MeterProvider
	var metricsHandler http.Handler
	if cfg.Telemetry.Enabled {
		tracerProvider, err = telemetry.Setup(ctx, telemetry.Config{
			ServiceName:  "api-gateway",
			OTLPEndpoint: cfg.Telemetry.OTLPEndpoint,
			SampleRatio:  cfg.Telemetry.SampleRatio,
		})
		if err != nil {
			log.Fatal("init telemetry", zap.Error(err))
		}
		// 指标支柱（阶段 2D 下半程）：与 trace 同一个 OTel SDK，区别是
		// pull 模式——进程内挂 /metrics 端点，Prometheus 定时来拉。
		// 全局 MeterProvider 一设，otelgrpc 客户端的 gRPC 指标也自动出现。
		meterProvider, metricsHandler, err = telemetry.SetupMetrics()
		if err != nil {
			log.Fatal("init metrics", zap.Error(err))
		}
		log.Info("telemetry enabled",
			zap.String("otlp_endpoint", cfg.Telemetry.OTLPEndpoint),
			zap.Float64("sample_ratio", cfg.Telemetry.SampleRatio))
	}

	// grpc.NewClient 是非阻塞的：即使 user-service / product-service 还没启动，
	// 这里也不会报错，真正的连接尝试发生在第一次 RPC 调用时。
	userClient, err := repository.NewUserClient(cfg.GRPCClient.UserServiceAddr)
	if err != nil {
		log.Fatal("failed to create user-service client", zap.Error(err))
	}
	productClient, err := repository.NewProductClient(cfg.GRPCClient.ProductServiceAddr)
	if err != nil {
		log.Fatal("failed to create product-service client", zap.Error(err))
	}
	orderClient, err := repository.NewOrderClient(cfg.GRPCClient.OrderServiceAddr)
	if err != nil {
		log.Fatal("failed to create order-service client", zap.Error(err))
	}

	svc := service.New(userClient, productClient, orderClient)
	h := handler.New(svc, log)
	engine := router.New(h, cfg.JWT.Secret, log, metricsHandler)

	addr := fmt.Sprintf(":%d", cfg.Server.APIGateway.HTTPPort)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: engine,
	}

	go func() {
		<-ctx.Done()
		log.Info("shutting down api-gateway")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Error("graceful shutdown failed", zap.Error(err))
		}
		// HTTP 停完再 flush trace：把批处理器里还没发出去的 span 冲给
		// Jaeger——不做这步，进程最后几秒的请求在链路系统里"凭空消失"。
		if tracerProvider != nil {
			if err := tracerProvider.Shutdown(shutdownCtx); err != nil {
				log.Error("trace provider shutdown", zap.Error(err))
			}
		}
		// pull 模式的指标没有待发缓冲，Shutdown 只是停掉收集协程、
		// 干净退出（不像 trace 那样关系数据丢失）。
		if meterProvider != nil {
			if err := meterProvider.Shutdown(shutdownCtx); err != nil {
				log.Error("meter provider shutdown", zap.Error(err))
			}
		}
	}()

	log.Info("api-gateway listening", zap.String("addr", addr))
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal("http server stopped with error", zap.Error(err))
	}
}
