// Command user-service 启动 user 服务的 gRPC server。
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

	"go-ecom-admin/internal/user/model"
	"go-ecom-admin/internal/user/repository"
	"go-ecom-admin/internal/user/service"
	userpb "go-ecom-admin/proto/user"
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

	// 阶段 1（项目初始化）允许在没有本地 MySQL 的情况下把服务跑起来：
	// 连不上就打一条 warn 日志继续、repo 留空，而不是 os.Exit —— 这样
	// `go run` 不会因为环境里还没有数据库就报错退出。到了真正接业务逻辑
	// 的阶段，这里应该改成连接失败就 Fatal，因为一个没有 DB 的 user-service
	// 毫无意义。
	var repo repository.Repository
	db, err := gorm.Open(mysql.Open(cfg.MySQL.DSN()), &gorm.Config{})
	if err != nil {
		log.Warn("failed to connect to MySQL, starting without a working repository (scaffold mode)", zap.Error(err))
	} else {
		if err := db.AutoMigrate(&model.User{}); err != nil {
			log.Warn("auto migrate failed", zap.Error(err))
		}
		repo = repository.NewGormRepository(db)
	}

	svc := service.New(repo, log)

	grpcServer := grpc.NewServer()
	userpb.RegisterUserServiceServer(grpcServer, svc)
	reflection.Register(grpcServer) // 方便本地用 grpcurl 调试，生产环境按需关闭

	addr := fmt.Sprintf(":%d", cfg.Server.UserService.GRPCPort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal("failed to listen", zap.String("addr", addr), zap.Error(err))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Info("shutting down user-service")
		grpcServer.GracefulStop()
	}()

	log.Info("user-service listening", zap.String("addr", addr))
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatal("grpc server stopped with error", zap.Error(err))
	}
}
