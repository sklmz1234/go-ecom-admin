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

	"go-ecom-admin/pkg/config"
	"go-ecom-admin/pkg/logger"

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

	svc := service.New(userClient, productClient)
	h := handler.New(svc, log)
	engine := router.New(h, cfg.JWT.Secret)

	addr := fmt.Sprintf(":%d", cfg.Server.APIGateway.HTTPPort)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: engine,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Info("shutting down api-gateway")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Error("graceful shutdown failed", zap.Error(err))
		}
	}()

	log.Info("api-gateway listening", zap.String("addr", addr))
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal("http server stopped with error", zap.Error(err))
	}
}
