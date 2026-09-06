// Package service 是 api-gateway 的编排层：把 REST 请求翻译成对下游 gRPC 服务的调用，
// 并把 gRPC 响应翻译回 REST DTO。以后如果一个 REST 接口需要聚合 user + product
// 两个服务的数据，也是在这一层做，而不是让 handler 直接调多个 repository。
package service

import (
	"context"
	"strconv"

	"google.golang.org/grpc/metadata"

	"go-ecom-admin/internal/gateway/model"
	"go-ecom-admin/internal/gateway/repository"
	productpb "go-ecom-admin/proto/product"
	userpb "go-ecom-admin/proto/user"
)

type Service struct {
	userClient    *repository.UserClient
	productClient *repository.ProductClient
}

func New(userClient *repository.UserClient, productClient *repository.ProductClient) *Service {
	return &Service{userClient: userClient, productClient: productClient}
}

// metadataKeyUserID 与 product-service 侧读取时使用的 key 保持一致。
// 身份走 metadata 而不是 proto 字段：身份是"横切关注点"，和 JWT 放在
// HTTP header 而不是塞 body 是同一个道理——每个 proto message 都加一个
// user_id 字段既重复又容易漏，metadata 由调用链统一注入，业务消息保持干净。
const metadataKeyUserID = "user_id"

// withCallerIdentity 把"当前登录用户是谁"附加到 outgoing context 上，随
// gRPC 调用一起传给下游服务。这是 gateway 作为"认证边界"的核心职责：
// JWT 在这里验完，下游服务只认 metadata 里的 user_id，不再各自验签。
func withCallerIdentity(ctx context.Context, userID uint64) context.Context {
	return metadata.AppendToOutgoingContext(ctx, metadataKeyUserID, strconv.FormatUint(userID, 10))
}

func (s *Service) GetUser(ctx context.Context, id uint64) (*model.UserDTO, error) {
	u, err := s.userClient.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	return userToDTO(u), nil
}

func (s *Service) Register(ctx context.Context, req model.RegisterRequest) (*model.UserDTO, error) {
	u, err := s.userClient.Register(ctx, req.Username, req.Email, req.Password)
	if err != nil {
		return nil, err
	}
	return userToDTO(u), nil
}

func (s *Service) Login(ctx context.Context, req model.LoginRequest) (*model.LoginResponseDTO, error) {
	token, u, err := s.userClient.Login(ctx, req.Username, req.Password)
	if err != nil {
		return nil, err
	}
	return &model.LoginResponseDTO{Token: token, User: userToDTO(u)}, nil
}

func (s *Service) GetProduct(ctx context.Context, id uint64) (*model.ProductDTO, error) {
	p, err := s.productClient.GetProduct(ctx, id)
	if err != nil {
		return nil, err
	}
	return productToDTO(p), nil
}

// CreateProduct / UpdateProduct / DeleteProduct 是写路径，必须带调用方身份——
// userID 由 handler 从 gin.Context 取（JWT 中间件已验过签），这里注入 metadata。
// 读路径（Get/List）是公开的，不需要身份。
func (s *Service) CreateProduct(ctx context.Context, userID uint64, req model.CreateProductRequest) (*model.ProductDTO, error) {
	// 元 -> 分：四舍五入到分，避免浮点数直接乘出现的精度误差被带进下游服务。
	priceCents := int64(req.PriceYuan*100 + 0.5)

	p, err := s.productClient.CreateProduct(withCallerIdentity(ctx, userID), req.Name, priceCents, req.Stock)
	if err != nil {
		return nil, err
	}
	return productToDTO(p), nil
}

func (s *Service) UpdateProduct(ctx context.Context, userID, id uint64, req model.UpdateProductRequest) (*model.ProductDTO, error) {
	priceCents := int64(req.PriceYuan*100 + 0.5)

	p, err := s.productClient.UpdateProduct(withCallerIdentity(ctx, userID), id, req.Name, priceCents, req.Stock)
	if err != nil {
		return nil, err
	}
	return productToDTO(p), nil
}

func (s *Service) DeleteProduct(ctx context.Context, userID, id uint64) error {
	return s.productClient.DeleteProduct(withCallerIdentity(ctx, userID), id)
}

func (s *Service) ListProducts(ctx context.Context, req model.ListProductsRequest) (*model.ListProductsResponse, error) {
	products, total, err := s.productClient.ListProducts(ctx, int32(req.Page), int32(req.PageSize))
	if err != nil {
		return nil, err
	}

	dtos := make([]*model.ProductDTO, 0, len(products))
	for _, p := range products {
		dtos = append(dtos, productToDTO(p))
	}

	return &model.ListProductsResponse{Products: dtos, Total: total}, nil
}

func userToDTO(u *userpb.User) *model.UserDTO {
	return &model.UserDTO{
		ID:        u.GetId(),
		Username:  u.GetUsername(),
		Email:     u.GetEmail(),
		CreatedAt: u.GetCreatedAt(),
	}
}

func productToDTO(p *productpb.Product) *model.ProductDTO {
	return &model.ProductDTO{
		ID:        p.GetId(),
		Name:      p.GetName(),
		PriceYuan: float64(p.GetPriceCents()) / 100,
		Stock:     p.GetStock(),
		OwnerID:   p.GetOwnerId(),
		CreatedAt: p.GetCreatedAt(),
	}
}
