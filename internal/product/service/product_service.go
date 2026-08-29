// Package service 承载 product 服务的业务逻辑，同时实现 gRPC 生成的 ProductServiceServer 接口。
package service

import (
	"context"

	"go.uber.org/zap"

	apperrors "go-ecom-admin/pkg/errors"

	"go-ecom-admin/internal/product/model"
	"go-ecom-admin/internal/product/repository"
	productpb "go-ecom-admin/proto/product"
)

type Service struct {
	productpb.UnimplementedProductServiceServer

	repo repository.Repository
	log  *zap.Logger
}

func New(repo repository.Repository, log *zap.Logger) *Service {
	return &Service{repo: repo, log: log}
}

func (s *Service) GetProduct(ctx context.Context, req *productpb.GetProductRequest) (*productpb.GetProductResponse, error) {
	p, err := s.repo.GetByID(ctx, req.GetId())
	if err != nil {
		s.log.Warn("get product failed", zap.Uint64("id", req.GetId()), zap.Error(err))
		return nil, apperrors.ToGRPCStatus(err)
	}
	return &productpb.GetProductResponse{Product: toProto(p)}, nil
}

func (s *Service) CreateProduct(ctx context.Context, req *productpb.CreateProductRequest) (*productpb.CreateProductResponse, error) {
	if req.GetName() == "" || req.GetPriceCents() < 0 {
		err := apperrors.InvalidArgument("name is required and price_cents must not be negative", nil)
		return nil, apperrors.ToGRPCStatus(err)
	}

	p := &model.Product{
		Name:       req.GetName(),
		PriceCents: req.GetPriceCents(),
		Stock:      req.GetStock(),
	}
	if err := s.repo.Create(ctx, p); err != nil {
		s.log.Warn("create product failed", zap.String("name", req.GetName()), zap.Error(err))
		return nil, apperrors.ToGRPCStatus(err)
	}

	return &productpb.CreateProductResponse{Product: toProto(p)}, nil
}

func (s *Service) ListProducts(ctx context.Context, req *productpb.ListProductsRequest) (*productpb.ListProductsResponse, error) {
	products, total, err := s.repo.List(ctx, int(req.GetPage()), int(req.GetPageSize()))
	if err != nil {
		s.log.Warn("list products failed", zap.Error(err))
		return nil, apperrors.ToGRPCStatus(err)
	}

	pbProducts := make([]*productpb.Product, 0, len(products))
	for _, p := range products {
		pbProducts = append(pbProducts, toProto(p))
	}

	return &productpb.ListProductsResponse{Products: pbProducts, Total: total}, nil
}

// UpdateProduct 是整体替换语义，和 proto 里 UpdateProductRequest 的注释呼应：
// 校验通过 -> repo.Update -> 再查一次拿到完整记录（主要是为了拿 CreatedAt，
// 这个字段不在请求里，直接用请求参数拼 proto 会漏掉）。
func (s *Service) UpdateProduct(ctx context.Context, req *productpb.UpdateProductRequest) (*productpb.UpdateProductResponse, error) {
	if req.GetName() == "" || req.GetPriceCents() < 0 {
		err := apperrors.InvalidArgument("name is required and price_cents must not be negative", nil)
		return nil, apperrors.ToGRPCStatus(err)
	}

	p := &model.Product{
		ID:         req.GetId(),
		Name:       req.GetName(),
		PriceCents: req.GetPriceCents(),
		Stock:      req.GetStock(),
	}
	if err := s.repo.Update(ctx, p); err != nil {
		s.log.Warn("update product failed", zap.Uint64("id", req.GetId()), zap.Error(err))
		return nil, apperrors.ToGRPCStatus(err)
	}

	updated, err := s.repo.GetByID(ctx, req.GetId())
	if err != nil {
		s.log.Warn("reload product after update failed", zap.Uint64("id", req.GetId()), zap.Error(err))
		return nil, apperrors.ToGRPCStatus(err)
	}

	return &productpb.UpdateProductResponse{Product: toProto(updated)}, nil
}

func (s *Service) DeleteProduct(ctx context.Context, req *productpb.DeleteProductRequest) (*productpb.DeleteProductResponse, error) {
	if err := s.repo.Delete(ctx, req.GetId()); err != nil {
		s.log.Warn("delete product failed", zap.Uint64("id", req.GetId()), zap.Error(err))
		return nil, apperrors.ToGRPCStatus(err)
	}
	return &productpb.DeleteProductResponse{}, nil
}

func toProto(p *model.Product) *productpb.Product {
	return &productpb.Product{
		Id:         p.ID,
		Name:       p.Name,
		PriceCents: p.PriceCents,
		Stock:      p.Stock,
		CreatedAt:  p.CreatedAt.Unix(),
	}
}
