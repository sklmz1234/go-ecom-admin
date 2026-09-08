package repository

import (
	"context"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	productpb "go-ecom-admin/proto/product"
)

// ProductClient 是 order-service 对 product-service 的依赖抽象。
// 定义为接口而不是直接用生成的 pb client：service 层的下单编排
// （扣库存→落单→失败补偿）是本项目 Saga 模式的核心教学场景，
// 单测必须能用 mock 精确控制每一步的成败。
type ProductClient interface {
	// DeductStock 成功时返回扣减结果（含 price_cents/name 快照）。
	DeductStock(ctx context.Context, productID uint64, quantity int32) (*productpb.DeductStockResponse, error)
	// RestoreStock 的 messageID 非空时走 product 侧幂等路径（outbox relay
	// 投递），为空是同步补偿的老语义——契约见 proto RestoreStockRequest。
	RestoreStock(ctx context.Context, productID uint64, quantity int32, messageID string) error
}

// defaultCallTimeout 与 api-gateway 的下游调用超时保持一致。
const defaultCallTimeout = 3 * time.Second

type grpcProductClient struct {
	client productpb.ProductServiceClient
}

// NewProductClient 拨号 product-service。
//
// 必须带 otelgrpc.NewClientHandler()：trace 上下文（traceparent）靠它注入
// gRPC metadata 接力给下游——漏了它，Jaeger 里下单链路会断在
// order→product 的进程边界上，变成两条独立 trace。
// 身份（user_id）的接力不在这里做：它按调用点在 service 层用
// pkg/identity.InjectOutgoing 显式注入（见 service.CreateOrder 的注释）。
func NewProductClient(target string) (*grpcProductClient, error) {
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, err
	}
	return &grpcProductClient{client: productpb.NewProductServiceClient(conn)}, nil
}

func (c *grpcProductClient) DeductStock(ctx context.Context, productID uint64, quantity int32) (*productpb.DeductStockResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()

	return c.client.DeductStock(ctx, &productpb.DeductStockRequest{
		ProductId: productID,
		Quantity:  quantity,
	})
}

func (c *grpcProductClient) RestoreStock(ctx context.Context, productID uint64, quantity int32, messageID string) error {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()

	_, err := c.client.RestoreStock(ctx, &productpb.RestoreStockRequest{
		ProductId:  productID,
		Quantity:   quantity,
		MessageId:  messageID,
	})
	return err
}
