// Package service 承载 user 服务的业务逻辑，同时实现 gRPC 生成的 UserServiceServer 接口。
//
// 设计决策：这一层是唯一知道「proto 消息 <-> 领域模型」怎么互转的地方。
// repository 只认识 model.User，gRPC 客户端只认识 userpb.User，
// 两边的转换集中在这里，不散落在 repository 或 handler 里。
package service

import (
	"context"

	"go.uber.org/zap"

	apperrors "go-ecom-admin/pkg/errors"

	"go-ecom-admin/internal/user/model"
	"go-ecom-admin/internal/user/repository"
	userpb "go-ecom-admin/proto/user"
)

// Service 实现 userpb.UserServiceServer。嵌入 UnimplementedUserServiceServer
// 是 protoc-gen-go-grpc 要求的前向兼容写法：以后 proto 里加新 rpc 时，
// 老的 Service 实现不会因为没实现新方法而编译失败。
type Service struct {
	userpb.UnimplementedUserServiceServer

	repo repository.Repository
	log  *zap.Logger
}

func New(repo repository.Repository, log *zap.Logger) *Service {
	return &Service{repo: repo, log: log}
}

func (s *Service) GetUser(ctx context.Context, req *userpb.GetUserRequest) (*userpb.GetUserResponse, error) {
	u, err := s.repo.GetByID(ctx, req.GetId())
	if err != nil {
		s.log.Warn("get user failed", zap.Uint64("id", req.GetId()), zap.Error(err))
		return nil, apperrors.ToGRPCStatus(err)
	}
	return &userpb.GetUserResponse{User: toProto(u)}, nil
}

func (s *Service) CreateUser(ctx context.Context, req *userpb.CreateUserRequest) (*userpb.CreateUserResponse, error) {
	if req.GetUsername() == "" || req.GetEmail() == "" {
		err := apperrors.InvalidArgument("username and email are required", nil)
		return nil, apperrors.ToGRPCStatus(err)
	}

	u := &model.User{Username: req.GetUsername(), Email: req.GetEmail()}
	if err := s.repo.Create(ctx, u); err != nil {
		s.log.Warn("create user failed", zap.String("username", req.GetUsername()), zap.Error(err))
		return nil, apperrors.ToGRPCStatus(err)
	}

	return &userpb.CreateUserResponse{User: toProto(u)}, nil
}

func toProto(u *model.User) *userpb.User {
	return &userpb.User{
		Id:        u.ID,
		Username:  u.Username,
		Email:     u.Email,
		CreatedAt: u.CreatedAt.Unix(),
	}
}
