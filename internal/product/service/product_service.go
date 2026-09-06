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
	// 先验身份再验参数：未认证的请求不应该得到任何关于参数的对错反馈，
	// 这也是大多数 API 的行为（401 优先于 400）。
	ownerID, err := userIDFromContext(ctx)
	if err != nil {
		return nil, apperrors.ToGRPCStatus(err)
	}

	if req.GetName() == "" || req.GetPriceCents() < 0 {
		err := apperrors.InvalidArgument("name is required and price_cents must not be negative", nil)
		return nil, apperrors.ToGRPCStatus(err)
	}

	p := &model.Product{
		Name:       req.GetName(),
		PriceCents: req.GetPriceCents(),
		Stock:      req.GetStock(),
		OwnerID:    ownerID,
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

// UpdateProduct 是整体替换语义，和 proto 里 UpdateProductRequest 的注释呼应。
// 流程：验身份 -> 校验参数 -> 查归属 -> repo.Update -> 再查一次拿完整记录。
//
// 为什么 Update 前多一次 GetByID（而不是把 owner_id 拼进 UPDATE 的 WHERE）：
// RowsAffected==0 无法区分"商品不存在"和"商品不是你的"，而客户端需要看到
// 不同的错误码（404 vs 403）——语义准确性值这一次查询。且这次查询走 2C 的
// 读缓存，热点商品的归属判断基本不碰 MySQL，成本可控。
func (s *Service) UpdateProduct(ctx context.Context, req *productpb.UpdateProductRequest) (*productpb.UpdateProductResponse, error) {
	ownerID, err := userIDFromContext(ctx)
	if err != nil {
		return nil, apperrors.ToGRPCStatus(err)
	}

	if req.GetName() == "" || req.GetPriceCents() < 0 {
		err := apperrors.InvalidArgument("name is required and price_cents must not be negative", nil)
		return nil, apperrors.ToGRPCStatus(err)
	}

	if err := s.checkOwnership(ctx, req.GetId(), ownerID); err != nil {
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
	ownerID, err := userIDFromContext(ctx)
	if err != nil {
		return nil, apperrors.ToGRPCStatus(err)
	}

	if err := s.checkOwnership(ctx, req.GetId(), ownerID); err != nil {
		return nil, apperrors.ToGRPCStatus(err)
	}

	if err := s.repo.Delete(ctx, req.GetId()); err != nil {
		s.log.Warn("delete product failed", zap.Uint64("id", req.GetId()), zap.Error(err))
		return nil, apperrors.ToGRPCStatus(err)
	}
	return &productpb.DeleteProductResponse{}, nil
}

// checkOwnership 是 Update/Delete 共用的归属校验：商品不存在 -> NotFound（404），
// 存在但属于别人 -> Forbidden（403）。把两种错误分开是安全语义的一部分——
// 这里选择不隐藏资源存在性（不拿 404 冒充 403），因为商品本来就是公开可读的，
// 存在性不是秘密；如果换成私密资源（如订单），就应该统一返回 404 防止枚举。
func (s *Service) checkOwnership(ctx context.Context, productID, ownerID uint64) error {
	p, err := s.repo.GetByID(ctx, productID)
	if err != nil {
		return err
	}
	if p.OwnerID != ownerID {
		s.log.Warn("ownership check failed",
			zap.Uint64("product_id", productID),
			zap.Uint64("owner_id", p.OwnerID),
			zap.Uint64("caller_id", ownerID),
		)
		return apperrors.Forbidden("you can only modify your own products", nil)
	}
	return nil
}

func toProto(p *model.Product) *productpb.Product {
	return &productpb.Product{
		Id:         p.ID,
		Name:       p.Name,
		PriceCents: p.PriceCents,
		Stock:      p.Stock,
		OwnerId:    p.OwnerID,
		CreatedAt:  p.CreatedAt.Unix(),
	}
}
