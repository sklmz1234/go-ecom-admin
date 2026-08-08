// Package service 是 api-gateway 的编排层：把 REST 请求翻译成对下游 gRPC 服务的调用，
// 并把 gRPC 响应翻译回 REST DTO。以后如果一个 REST 接口需要聚合 user + product
// 两个服务的数据，也是在这一层做，而不是让 handler 直接调多个 repository。
package service

import (
	"context"

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

func (s *Service) GetUser(ctx context.Context, id uint64) (*model.UserDTO, error) {
	u, err := s.userClient.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	return userToDTO(u), nil
}

func (s *Service) CreateUser(ctx context.Context, req model.CreateUserRequest) (*model.UserDTO, error) {
	u, err := s.userClient.CreateUser(ctx, req.Username, req.Email)
	if err != nil {
		return nil, err
	}
	return userToDTO(u), nil
}

func (s *Service) GetProduct(ctx context.Context, id uint64) (*model.ProductDTO, error) {
	p, err := s.productClient.GetProduct(ctx, id)
	if err != nil {
		return nil, err
	}
	return productToDTO(p), nil
}

func (s *Service) CreateProduct(ctx context.Context, req model.CreateProductRequest) (*model.ProductDTO, error) {
	// 元 -> 分：四舍五入到分，避免浮点数直接乘出现的精度误差被带进下游服务。
	priceCents := int64(req.PriceYuan*100 + 0.5)

	p, err := s.productClient.CreateProduct(ctx, req.Name, priceCents, req.Stock)
	if err != nil {
		return nil, err
	}
	return productToDTO(p), nil
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
		CreatedAt: p.GetCreatedAt(),
	}
}
