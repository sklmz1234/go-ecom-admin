// product 服务的 service 层单元测试，策略与 internal/user/service 一致：
// mockery 生成的 MockRepository 注入假存储，只测业务规则与错误翻译。
//
// product 特有的考点：
//   - UpdateProduct 是「整体替换」语义——先 Update 再 GetByID 重载完整记录
//     （拿请求里没有的 CreatedAt），测试要证明 reload 真的发生了；
//   - 写路径（Create/Update/Delete）都要通过 gRPC metadata 的 user_id 做
//     身份与归属校验，测试用 ctxWithUserID 模拟 api-gateway 注入的 metadata。
package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	apperrors "go-ecom-admin/pkg/errors"

	"go-ecom-admin/internal/product/model"
	"go-ecom-admin/internal/product/repository/mocks"
	productpb "go-ecom-admin/proto/product"
)

func newTestService(t *testing.T) (*Service, *mocks.MockRepository) {
	t.Helper()
	repo := mocks.NewMockRepository(t)
	return New(repo, zaptest.NewLogger(t)), repo
}

// ctxWithUserID 模拟 api-gateway 验完 JWT 后注入的 incoming metadata——
// 单测里不经过真实的 gRPC 传输，直接手工构造同样的 context 形态。
func ctxWithUserID(uid uint64) context.Context {
	md := metadata.Pairs(metadataKeyUserID, strconv.FormatUint(uid, 10))
	return metadata.NewIncomingContext(context.Background(), md)
}

// TestCreateProduct_Validation 覆盖参数校验；mock 零期望同时证明
// 非法输入不会触达数据库。
func TestCreateProduct_Validation(t *testing.T) {
	tests := []struct {
		name string
		req  *productpb.CreateProductRequest
	}{
		{"缺少名称", &productpb.CreateProductRequest{Name: "", PriceCents: 100}},
		{"价格为负", &productpb.CreateProductRequest{Name: "机械键盘", PriceCents: -1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTestService(t)

			resp, err := svc.CreateProduct(ctxWithUserID(42), tt.req)

			require.Error(t, err)
			assert.Nil(t, resp)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

// TestCreateProduct_Unauthenticated 验证零信任底线：metadata 里没有 user_id
// 的请求一律拒绝（Unauthenticated），不允许创建"无主商品"。
func TestCreateProduct_Unauthenticated(t *testing.T) {
	svc, _ := newTestService(t)

	resp, err := svc.CreateProduct(context.Background(), &productpb.CreateProductRequest{
		Name: "机械键盘", PriceCents: 29900,
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestCreateProduct_Success(t *testing.T) {
	svc, repo := newTestService(t)

	repo.EXPECT().Create(mock.Anything, mock.Anything).
		Run(func(_ context.Context, p *model.Product) {
			p.ID = 7 // 模拟自增主键回填
			// 归属必须在落库前就已经由 service 从 metadata 写入——
			// 在 Run 里断言，比事后检查返回值更能证明写入路径正确。
			assert.Equal(t, uint64(42), p.OwnerID)
		}).
		Return(nil)

	resp, err := svc.CreateProduct(ctxWithUserID(42), &productpb.CreateProductRequest{
		Name: "机械键盘", PriceCents: 29900, Stock: 10,
	})

	require.NoError(t, err)
	assert.Equal(t, uint64(7), resp.GetProduct().GetId())
	assert.Equal(t, "机械键盘", resp.GetProduct().GetName())
	assert.Equal(t, int64(29900), resp.GetProduct().GetPriceCents())
	assert.Equal(t, int32(10), resp.GetProduct().GetStock())
	assert.Equal(t, uint64(42), resp.GetProduct().GetOwnerId())
}

// TestGetProduct_NotFound 读路径是公开的，不需要 metadata。
func TestGetProduct_NotFound(t *testing.T) {
	svc, repo := newTestService(t)
	repo.EXPECT().GetByID(mock.Anything, uint64(404)).
		Return(nil, apperrors.NotFound("product not found", nil))

	resp, err := svc.GetProduct(context.Background(), &productpb.GetProductRequest{Id: 404})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// TestUpdateProduct_NotFound 商品不存在时归属查询直接返回 NotFound，
// Update 根本不会执行（mock 未设 Update 期望，调了就算失败）。
func TestUpdateProduct_NotFound(t *testing.T) {
	svc, repo := newTestService(t)
	repo.EXPECT().GetByID(mock.Anything, uint64(404)).
		Return(nil, apperrors.NotFound("product not found", nil))

	resp, err := svc.UpdateProduct(ctxWithUserID(42), &productpb.UpdateProductRequest{
		Id: 404, Name: "机械键盘", PriceCents: 29900,
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// TestUpdateProduct_Forbidden 商品存在但属于别人 -> PermissionDenied（403），
// 且 Update 不会执行。这是本次归属校验的核心用例。
func TestUpdateProduct_Forbidden(t *testing.T) {
	svc, repo := newTestService(t)
	repo.EXPECT().GetByID(mock.Anything, uint64(7)).
		Return(&model.Product{ID: 7, Name: "机械键盘", OwnerID: 99}, nil)

	resp, err := svc.UpdateProduct(ctxWithUserID(42), &productpb.UpdateProductRequest{
		Id: 7, Name: "改名了", PriceCents: 19900,
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// TestUpdateProduct_Unauthenticated 没带身份的更新请求在归属查询之前就被拒。
func TestUpdateProduct_Unauthenticated(t *testing.T) {
	svc, _ := newTestService(t)

	resp, err := svc.UpdateProduct(context.Background(), &productpb.UpdateProductRequest{
		Id: 7, Name: "改名了", PriceCents: 19900,
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// TestUpdateProduct_Success 证明「查归属 -> Update -> 重载」的完整设计：
// 响应里的 CreatedAt 来自重载的记录（请求里没有这个字段），而不是零值。
func TestUpdateProduct_Success(t *testing.T) {
	svc, repo := newTestService(t)

	created := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	// 第一次 GetByID 是归属校验：商品属于 42，放行。
	repo.EXPECT().GetByID(mock.Anything, uint64(7)).
		Return(&model.Product{ID: 7, Name: "机械键盘", OwnerID: 42}, nil).Once()
	repo.EXPECT().Update(mock.Anything, mock.Anything).Return(nil)
	// Update 之后 service 必须再调一次 GetByID 拿完整记录——这个 EXPECT
	// 同时是断言：如果 service 只 Update 不重载，mockery 会因未满足的
	// 期望直接让测试失败。
	repo.EXPECT().GetByID(mock.Anything, uint64(7)).
		Return(&model.Product{
			ID: 7, Name: "机械键盘 Pro", PriceCents: 39900, Stock: 5, OwnerID: 42, CreatedAt: created,
		}, nil).Once()

	resp, err := svc.UpdateProduct(ctxWithUserID(42), &productpb.UpdateProductRequest{
		Id: 7, Name: "机械键盘 Pro", PriceCents: 39900, Stock: 5,
	})

	require.NoError(t, err)
	assert.Equal(t, "机械键盘 Pro", resp.GetProduct().GetName())
	assert.Equal(t, created.Unix(), resp.GetProduct().GetCreatedAt())
	assert.Equal(t, uint64(42), resp.GetProduct().GetOwnerId())
}

func TestDeleteProduct_NotFound(t *testing.T) {
	svc, repo := newTestService(t)
	repo.EXPECT().GetByID(mock.Anything, uint64(404)).
		Return(nil, apperrors.NotFound("product not found", nil))

	resp, err := svc.DeleteProduct(ctxWithUserID(42), &productpb.DeleteProductRequest{Id: 404})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// TestDeleteProduct_Forbidden 别人的商品删不掉。
func TestDeleteProduct_Forbidden(t *testing.T) {
	svc, repo := newTestService(t)
	repo.EXPECT().GetByID(mock.Anything, uint64(7)).
		Return(&model.Product{ID: 7, Name: "机械键盘", OwnerID: 99}, nil)

	resp, err := svc.DeleteProduct(ctxWithUserID(42), &productpb.DeleteProductRequest{Id: 7})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestDeleteProduct_Success(t *testing.T) {
	svc, repo := newTestService(t)
	repo.EXPECT().GetByID(mock.Anything, uint64(7)).
		Return(&model.Product{ID: 7, Name: "机械键盘", OwnerID: 42}, nil)
	repo.EXPECT().Delete(mock.Anything, uint64(7)).Return(nil)

	resp, err := svc.DeleteProduct(ctxWithUserID(42), &productpb.DeleteProductRequest{Id: 7})

	require.NoError(t, err)
	assert.NotNil(t, resp)
}

// TestListProducts 验证 model -> proto 的批量转换和 total 透传。
func TestListProducts(t *testing.T) {
	svc, repo := newTestService(t)
	repo.EXPECT().List(mock.Anything, 1, 10).Return([]*model.Product{
		{ID: 1, Name: "机械键盘", PriceCents: 29900, Stock: 10, OwnerID: 42},
		{ID: 2, Name: "显示器", PriceCents: 199900, Stock: 3, OwnerID: 7},
	}, int64(25), nil)

	resp, err := svc.ListProducts(context.Background(), &productpb.ListProductsRequest{Page: 1, PageSize: 10})

	require.NoError(t, err)
	require.Len(t, resp.GetProducts(), 2)
	assert.Equal(t, int64(25), resp.GetTotal())
	assert.Equal(t, "显示器", resp.GetProducts()[1].GetName())
	assert.Equal(t, uint64(7), resp.GetProducts()[1].GetOwnerId())
}
