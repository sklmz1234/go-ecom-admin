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

	// 注册、登录和密码校验都依赖持久化用户数据，因此服务启动时必须先确认
	// 数据库连接和表结构准备完成。初始化失败时立即结束进程，避免服务在无法
	// 正常处理请求的状态下继续监听，直到请求到来后才暴露数据库不可用的问题。
	// TranslateError: true 让 GORM 把驱动的方言错误（例如 MySQL 的 1062
	// 唯一键冲突）翻译成统一的 gorm.ErrDuplicatedKey——不开这个开关，
	// repository.Create 里的 errors.Is(err, gorm.ErrDuplicatedKey) 永远
	// 不会命中，重复用户名会被错误地当成 Internal(500) 而不是 409。
	db, err := gorm.Open(mysql.Open(cfg.MySQL.DSN()), &gorm.Config{TranslateError: true})
	if err != nil {
		log.Fatal("failed to connect to MySQL", zap.Error(err))
	}
	if err := db.AutoMigrate(&model.User{}); err != nil {
		log.Fatal("auto migrate failed", zap.Error(err))
	}
	repo := repository.NewGormRepository(db)

	svc := service.New(repo, log, cfg.JWT.Secret, cfg.JWT.ExpireHours)

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
