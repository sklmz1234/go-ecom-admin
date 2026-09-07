// Package service 承载 order 服务的业务逻辑，实现 gRPC 生成的 OrderServiceServer。
//
// 本包的核心是 CreateOrder 的下单编排——Saga 模式的最小形态：
// 下单 = product-service 扣库存 + order-service 落订单，跨两个服务两个库，
// 没有本地事务可救，用"顺序调用 + 失败补偿"保证最终一致：
// 任一环节失败，把所有已扣的库存逐项 RestoreStock 回补。
// 补偿本身也可能失败——生产解法是重试 + 对账（本地消息表，留作阶段 4），
// 这里做到补偿为止，补偿失败记 ERROR 日志留对账线索。
package service

import (
	"context"

	"go.uber.org/zap"

	apperrors "go-ecom-admin/pkg/errors"
	"go-ecom-admin/pkg/identity"

	"go-ecom-admin/internal/order/model"
	"go-ecom-admin/internal/order/repository"
	orderpb "go-ecom-admin/proto/order"
)

type Service struct {
	orderpb.UnimplementedOrderServiceServer

	repo     repository.Repository
	products repository.ProductClient
	log      *zap.Logger
}

func New(repo repository.Repository, products repository.ProductClient, log *zap.Logger) *Service {
	return &Service{repo: repo, products: products, log: log}
}

// deductedItem 记录一笔已成功扣减，补偿时按它逐项回补。
type deductedItem struct {
	productID uint64
	quantity  int32
}

func (s *Service) CreateOrder(ctx context.Context, req *orderpb.CreateOrderRequest) (*orderpb.CreateOrderResponse, error) {
	// 先验身份再验参数（401 优先于 400），与 product-service 同一惯例。
	callerID, err := identity.FromIncoming(ctx)
	if err != nil {
		return nil, apperrors.ToGRPCStatus(err)
	}

	if len(req.GetItems()) == 0 {
		return nil, apperrors.ToGRPCStatus(apperrors.InvalidArgument("items must not be empty", nil))
	}
	for _, it := range req.GetItems() {
		if it.GetProductId() == 0 || it.GetQuantity() <= 0 {
			return nil, apperrors.ToGRPCStatus(apperrors.InvalidArgument("product_id is required and quantity must be positive", nil))
		}
	}

	// 身份接力：metadata 不会自动从 incoming 透传到 outgoing，调
	// product-service 前必须显式再注入一次——下游靠它做"必须带身份"的
	// 零信任校验，同时扣减日志里能记录是谁的下单触发的（审计）。
	callCtx := identity.InjectOutgoing(ctx, callerID)

	// 第一步：逐项扣库存。同一商品在 items 里出现多次不去重合并——
	// 逐次扣减语义等价（条件更新各自原子），补偿也按扣减记录逐笔回补，
	// 逻辑比分组聚合更直白；聚合优化留给真实业务量级时再谈。
	var deducted []deductedItem
	order := &model.Order{
		UserID: callerID,
		Status: model.StatusPending,
	}
	for _, it := range req.GetItems() {
		resp, err := s.products.DeductStock(callCtx, it.GetProductId(), it.GetQuantity())
		if err != nil {
			// 第 N 项扣减失败：回补前 N-1 项，然后把原始错误（库存不足的
			// FailedPrecondition / 商品不存在的 NotFound）透传给调用方。
			s.compensate(callCtx, deducted)
			return nil, err
		}
		deducted = append(deducted, deductedItem{productID: it.GetProductId(), quantity: it.GetQuantity()})
		order.Items = append(order.Items, model.OrderItem{
			ProductID:      it.GetProductId(),
			ProductName:    resp.GetName(),
			Quantity:       it.GetQuantity(),
			UnitPriceCents: resp.GetPriceCents(), // 下单时刻价格快照
		})
		order.TotalCents += resp.GetPriceCents() * int64(it.GetQuantity())
	}

	// 第二步：落订单（orders + order_items 单事务）。失败同样全额回补。
	if err := s.repo.Create(ctx, order); err != nil {
		s.log.Error("create order failed after stock deducted, compensating",
			zap.Uint64("user_id", callerID), zap.Error(err))
		s.compensate(callCtx, deducted)
		return nil, apperrors.ToGRPCStatus(err)
	}

	s.log.Info("order created",
		zap.Uint64("order_id", order.ID),
		zap.Uint64("user_id", callerID),
		zap.Int64("total_cents", order.TotalCents),
		zap.Int("items", len(order.Items)),
	)
	return &orderpb.CreateOrderResponse{Order: toProto(order)}, nil
}

// compensate 逐项回补已扣库存。补偿失败的后果是"库存少了但订单没建成"——
// 数据不一致，必须留下足够醒目的日志供对账（生产环境还会配告警）。
// 补偿不回传错误：编排已经失败，调用方只需要知道原始失败原因。
func (s *Service) compensate(ctx context.Context, deducted []deductedItem) {
	for _, d := range deducted {
		if err := s.products.RestoreStock(ctx, d.productID, d.quantity); err != nil {
			s.log.Error("COMPENSATION FAILED: stock not restored, manual reconciliation required",
				zap.Uint64("product_id", d.productID),
				zap.Int32("quantity", d.quantity),
				zap.Error(err),
			)
		}
	}
}

func (s *Service) GetOrder(ctx context.Context, req *orderpb.GetOrderRequest) (*orderpb.GetOrderResponse, error) {
	callerID, err := identity.FromIncoming(ctx)
	if err != nil {
		return nil, apperrors.ToGRPCStatus(err)
	}

	o, err := s.repo.GetByID(ctx, req.GetId())
	if err != nil {
		return nil, apperrors.ToGRPCStatus(err)
	}

	// 越权查订单返回 404 而不是 403：403 等于告诉攻击者"这个订单号存在，
	// 只是不是你的"，404 不暴露存在性（订单号可枚举，存在性即信息）。
	// 与商品归属的 403 语义差异是有意的：商品公开可读、存在性不是秘密，
	// 订单是私密资源，两个决策同源同理。
	if o.UserID != callerID {
		s.log.Warn("order access denied",
			zap.Uint64("order_id", o.ID),
			zap.Uint64("owner_id", o.UserID),
			zap.Uint64("caller_id", callerID),
		)
		return nil, apperrors.ToGRPCStatus(apperrors.NotFound("order not found", nil))
	}

	return &orderpb.GetOrderResponse{Order: toProto(o)}, nil
}

// ListMyOrders 只返回调用者自己的订单，user_id 过滤从 repo 层就带上，
// 不存在"查全部再过滤"的越权窗口。
func (s *Service) ListMyOrders(ctx context.Context, req *orderpb.ListMyOrdersRequest) (*orderpb.ListMyOrdersResponse, error) {
	callerID, err := identity.FromIncoming(ctx)
	if err != nil {
		return nil, apperrors.ToGRPCStatus(err)
	}

	orders, total, err := s.repo.ListByUser(ctx, callerID, int(req.GetPage()), int(req.GetPageSize()))
	if err != nil {
		return nil, apperrors.ToGRPCStatus(err)
	}

	pbOrders := make([]*orderpb.Order, 0, len(orders))
	for _, o := range orders {
		pbOrders = append(pbOrders, toProto(o))
	}
	return &orderpb.ListMyOrdersResponse{Orders: pbOrders, Total: total}, nil
}

// CancelOrder 留待阶段 3b 实现（PENDING → CANCELLED + RestoreStock 回补），
// 此处依赖内嵌的 UnimplementedOrderServiceServer 返回 Unimplemented。

func toProto(o *model.Order) *orderpb.Order {
	items := make([]*orderpb.OrderItem, 0, len(o.Items))
	for _, it := range o.Items {
		items = append(items, &orderpb.OrderItem{
			ProductId:      it.ProductID,
			ProductName:    it.ProductName,
			Quantity:       it.Quantity,
			UnitPriceCents: it.UnitPriceCents,
		})
	}
	return &orderpb.Order{
		Id:         o.ID,
		UserId:     o.UserID,
		Status:     statusToProto(o.Status),
		TotalCents: o.TotalCents,
		Items:      items,
		CreatedAt:  o.CreatedAt.Unix(),
	}
}

func statusToProto(status string) orderpb.OrderStatus {
	switch status {
	case model.StatusPending:
		return orderpb.OrderStatus_ORDER_STATUS_PENDING
	case model.StatusPaid:
		return orderpb.OrderStatus_ORDER_STATUS_PAID
	case model.StatusCancelled:
		return orderpb.OrderStatus_ORDER_STATUS_CANCELLED
	default:
		return orderpb.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}
