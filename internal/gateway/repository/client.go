// Package repository 是 api-gateway 的"数据访问层"——只不过它的数据源不是数据库，
// 而是别的微服务。这是 Repository 模式的一个常被忽略的泛化：只要一样东西负责
// "把外部数据取回来"，无论底层是 SQL、Redis 还是另一个服务的 gRPC 接口，
// 都可以用同一个接口 + 实现的模式封装，让 service 层不用关心通信细节
// （拨号、超时、重试都缩在这里）。
package repository

import (
	"context"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	orderpb "go-ecom-admin/proto/order"
	productpb "go-ecom-admin/proto/product"
	userpb "go-ecom-admin/proto/user"
)

// defaultCallTimeout 给每次下游调用设置超时，避免某个后端服务卡死时
// 把 api-gateway 的请求协程也一起拖死。
const defaultCallTimeout = 3 * time.Second

// UserClient 封装对 user-service 的 gRPC 调用。
type UserClient struct {
	client userpb.UserServiceClient
}

// otelStatsHandler 是两个 gRPC 客户端共用的 stats handler：每次 RPC 把
// 当前 trace 上下文（traceparent）注入 gRPC metadata，并在客户端侧记一条
// 出站 span——没有它，网关的 span 和下游服务的 span 就断在进程边界上，
// Jaeger 里出现的是两条独立链路而不是一条瀑布。
// 用 stats.Handler 而不是旧的 UnaryClientInterceptor：interceptor 方案拿
// 不到连接级事件，且官方已标记 deprecated。
var otelStatsHandler = otelgrpc.NewClientHandler()

// NewUserClient 用 target（形如 "127.0.0.1:9001"）拨号 user-service。
// grpc.NewClient 是非阻塞的——它只做地址解析和惰性连接管理，不会在这里
// 真的发起网络连接，所以即使 user-service 还没启动，api-gateway 也能正常起来
// （请求到来时才会真正建连，失败了也只是那一次调用报错）。
func NewUserClient(target string) (*UserClient, error) {
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelStatsHandler),
	)
	if err != nil {
		return nil, err
	}
	return &UserClient{client: userpb.NewUserServiceClient(conn)}, nil
}

func (c *UserClient) GetUser(ctx context.Context, id uint64) (*userpb.User, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()

	resp, err := c.client.GetUser(ctx, &userpb.GetUserRequest{Id: id})
	if err != nil {
		return nil, err
	}
	return resp.GetUser(), nil
}

func (c *UserClient) Register(ctx context.Context, username, email, password string) (*userpb.User, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()

	resp, err := c.client.Register(ctx, &userpb.RegisterRequest{Username: username, Email: email, Password: password})
	if err != nil {
		return nil, err
	}
	return resp.GetUser(), nil
}

// Login 返回 (token, user)，token 的生成在 user-service 完成——gateway
// 只负责透传，不知道也不需要知道签名算法/密钥。
func (c *UserClient) Login(ctx context.Context, username, password string) (string, *userpb.User, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()

	resp, err := c.client.Login(ctx, &userpb.LoginRequest{Username: username, Password: password})
	if err != nil {
		return "", nil, err
	}
	return resp.GetToken(), resp.GetUser(), nil
}

// ProductClient 封装对 product-service 的 gRPC 调用。
type ProductClient struct {
	client productpb.ProductServiceClient
}

func NewProductClient(target string) (*ProductClient, error) {
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelStatsHandler),
	)
	if err != nil {
		return nil, err
	}
	return &ProductClient{client: productpb.NewProductServiceClient(conn)}, nil
}

func (c *ProductClient) GetProduct(ctx context.Context, id uint64) (*productpb.Product, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()

	resp, err := c.client.GetProduct(ctx, &productpb.GetProductRequest{Id: id})
	if err != nil {
		return nil, err
	}
	return resp.GetProduct(), nil
}

func (c *ProductClient) CreateProduct(ctx context.Context, name string, priceCents int64, stock int32) (*productpb.Product, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()

	resp, err := c.client.CreateProduct(ctx, &productpb.CreateProductRequest{
		Name: name, PriceCents: priceCents, Stock: stock,
	})
	if err != nil {
		return nil, err
	}
	return resp.GetProduct(), nil
}

func (c *ProductClient) UpdateProduct(ctx context.Context, id uint64, name string, priceCents int64, stock int32) (*productpb.Product, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()

	resp, err := c.client.UpdateProduct(ctx, &productpb.UpdateProductRequest{
		Id: id, Name: name, PriceCents: priceCents, Stock: stock,
	})
	if err != nil {
		return nil, err
	}
	return resp.GetProduct(), nil
}

func (c *ProductClient) DeleteProduct(ctx context.Context, id uint64) error {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()

	_, err := c.client.DeleteProduct(ctx, &productpb.DeleteProductRequest{Id: id})
	return err
}

func (c *ProductClient) ListProducts(ctx context.Context, page, pageSize int32) ([]*productpb.Product, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()

	resp, err := c.client.ListProducts(ctx, &productpb.ListProductsRequest{Page: page, PageSize: pageSize})
	if err != nil {
		return nil, 0, err
	}
	return resp.GetProducts(), resp.GetTotal(), nil
}

// OrderClient 封装对 order-service 的 gRPC 调用（阶段 3）。
type OrderClient struct {
	client orderpb.OrderServiceClient
}

func NewOrderClient(target string) (*OrderClient, error) {
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelStatsHandler),
	)
	if err != nil {
		return nil, err
	}
	return &OrderClient{client: orderpb.NewOrderServiceClient(conn)}, nil
}

// CreateOrder 的 ctx 必须已由 service 层注入调用方身份（pkg/identity）——
// order-service 的零信任校验要求所有方法都带 user_id。
func (c *OrderClient) CreateOrder(ctx context.Context, items []*orderpb.CreateOrderItem) (*orderpb.Order, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()

	resp, err := c.client.CreateOrder(ctx, &orderpb.CreateOrderRequest{Items: items})
	if err != nil {
		return nil, err
	}
	return resp.GetOrder(), nil
}

func (c *OrderClient) GetOrder(ctx context.Context, id uint64) (*orderpb.Order, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()

	resp, err := c.client.GetOrder(ctx, &orderpb.GetOrderRequest{Id: id})
	if err != nil {
		return nil, err
	}
	return resp.GetOrder(), nil
}

func (c *OrderClient) ListMyOrders(ctx context.Context, page, pageSize int32) ([]*orderpb.Order, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()

	resp, err := c.client.ListMyOrders(ctx, &orderpb.ListMyOrdersRequest{Page: page, PageSize: pageSize})
	if err != nil {
		return nil, 0, err
	}
	return resp.GetOrders(), resp.GetTotal(), nil
}
