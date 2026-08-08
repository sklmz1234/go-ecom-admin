// Package repository 是 api-gateway 的"数据访问层"——只不过它的数据源不是数据库，
// 而是别的微服务。这是 Repository 模式的一个常被忽略的泛化：只要一样东西负责
// "把外部数据取回来"，无论底层是 SQL、Redis 还是另一个服务的 gRPC 接口，
// 都可以用同一个接口 + 实现的模式封装，让 service 层不用关心通信细节
// （拨号、超时、重试都缩在这里）。
package repository

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

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

// NewUserClient 用 target（形如 "127.0.0.1:9001"）拨号 user-service。
// grpc.NewClient 是非阻塞的——它只做地址解析和惰性连接管理，不会在这里
// 真的发起网络连接，所以即使 user-service 还没启动，api-gateway 也能正常起来
// （请求到来时才会真正建连，失败了也只是那一次调用报错）。
func NewUserClient(target string) (*UserClient, error) {
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
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

func (c *UserClient) CreateUser(ctx context.Context, username, email string) (*userpb.User, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()

	resp, err := c.client.CreateUser(ctx, &userpb.CreateUserRequest{Username: username, Email: email})
	if err != nil {
		return nil, err
	}
	return resp.GetUser(), nil
}

// ProductClient 封装对 product-service 的 gRPC 调用。
type ProductClient struct {
	client productpb.ProductServiceClient
}

func NewProductClient(target string) (*ProductClient, error) {
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
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

func (c *ProductClient) ListProducts(ctx context.Context, page, pageSize int32) ([]*productpb.Product, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()

	resp, err := c.client.ListProducts(ctx, &productpb.ListProductsRequest{Page: page, PageSize: pageSize})
	if err != nil {
		return nil, 0, err
	}
	return resp.GetProducts(), resp.GetTotal(), nil
}
