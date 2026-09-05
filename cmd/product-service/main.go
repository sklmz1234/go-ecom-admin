// Command product-service 启动 product 服务的 gRPC server。
// 结构与 cmd/user-service/main.go 完全对称，理由见那边的注释。
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"go-ecom-admin/pkg/cache"
	"go-ecom-admin/pkg/config"
	"go-ecom-admin/pkg/database"
	"go-ecom-admin/pkg/logger"

	"go-ecom-admin/internal/product/model"
	"go-ecom-admin/internal/product/repository"
	"go-ecom-admin/internal/product/service"
	productpb "go-ecom-admin/proto/product"
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

	// 和 user-service 一样：现在依赖真实数据（库存/价格都要能改），
	// 连不上数据库直接 Fatal，不再走"scaffold mode"的优雅降级。
	db, err := gorm.Open(mysql.Open(cfg.MySQL.DSN()), &gorm.Config{TranslateError: true}) // TranslateError: 驱动方言错误(如 MySQL 1062)→gorm.ErrDuplicatedKey 等统一错误
	if err != nil {
		log.Fatal("failed to connect to MySQL", zap.Error(err))
	}
	// 带锁迁移，理由见 cmd/user-service/main.go：多副本并发建表竞态。
	if err := database.Migrate(db, 30*time.Second, &model.Product{}); err != nil {
		log.Fatal("auto migrate failed", zap.Error(err))
	}
	repo := repository.NewGormRepository(db)

	// 缓存装饰器（阶段 2C）：Redis 可用就把 gorm 实现包进 cache-aside 装饰器，
	// service 层拿到的仍是同一个 Repository 接口，零改动。
	// Redis 连不上时降级为直连 MySQL——缓存是加速层不是正确性依赖，
	// 缓存挂了服务应该变慢而不是变挂（和装饰器里"Redis 故障降级回源"同一原则）。
	rdb, err := cache.New(cache.Config{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err != nil {
		log.Warn("redis unavailable, product cache disabled (falling back to direct MySQL)", zap.Error(err))
	} else {
		defer rdb.Close()
		repo = repository.NewCachedRepository(repo, rdb)
		log.Info("product cache enabled", zap.String("redis", cfg.Redis.Addr))
	}

	svc := service.New(repo, log)

	grpcServer := grpc.NewServer()
	productpb.RegisterProductServiceServer(grpcServer, svc)
	reflection.Register(grpcServer)

	// 同 user-service：注册标准健康检查服务，供 K8s 原生 grpc 探针使用，
	// 详见 cmd/user-service/main.go 里的注释。
	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthSrv)
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	addr := fmt.Sprintf(":%d", cfg.Server.ProductService.GRPCPort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal("failed to listen", zap.String("addr", addr), zap.Error(err))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Info("shutting down product-service")
		grpcServer.GracefulStop()
	}()

	log.Info("product-service listening", zap.String("addr", addr))
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatal("grpc server stopped with error", zap.Error(err))
	}
}
