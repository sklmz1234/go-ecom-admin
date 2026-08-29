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

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"go-ecom-admin/pkg/config"
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
	db, err := gorm.Open(mysql.Open(cfg.MySQL.DSN()), &gorm.Config{})
	if err != nil {
		log.Fatal("failed to connect to MySQL", zap.Error(err))
	}
	if err := db.AutoMigrate(&model.Product{}); err != nil {
		log.Fatal("auto migrate failed", zap.Error(err))
	}
	repo := repository.NewGormRepository(db)

	svc := service.New(repo, log)

	grpcServer := grpc.NewServer()
	productpb.RegisterProductServiceServer(grpcServer, svc)
	reflection.Register(grpcServer)

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
