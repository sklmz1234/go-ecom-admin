// user 服务的 service 层单元测试。
//
// 测试策略：通过 mockery 生成的 MockRepository 注入假的存储实现，只验证
// 业务规则（参数校验、bcrypt、防用户枚举、JWT 签发、AppError -> gRPC
// status 的翻译），不碰真实数据库——GORM 行为由 repository 层的 sqlite
// 测试负责，两者合起来才是完整的测试网。
//
// 断言选型：require（失败立即终止）用于「拿到响应/错误」这类后续断言的
// 前置条件；assert（失败继续）用于可以并列检查的字段。日志用
// zaptest.NewLogger(t)：只在测试失败时输出，且计入 t.Log 方便排查。
package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apperrors "go-ecom-admin/pkg/errors"
	appjwt "go-ecom-admin/pkg/jwt"

	"go-ecom-admin/internal/user/model"
	"go-ecom-admin/internal/user/repository/mocks"
	userpb "go-ecom-admin/proto/user"
)

const (
	testJWTSecret = "unit-test-secret"
	testUsername  = "sklmz"
	testEmail     = "sklmz@example.com"
	testPassword  = "right-pass"
)

func newTestService(t *testing.T) (*Service, *mocks.MockRepository) {
	t.Helper()
	repo := mocks.NewMockRepository(t)
	return New(repo, zaptest.NewLogger(t), testJWTSecret, 1), repo
}

// storedUser 构造一条「数据库里的用户」。bcrypt.MinCost 而非 DefaultCost：
// 哈希成本从 ~100ms 降到 ~1ms，三个用例就能省几百毫秒——测试速度也是工程指标。
func storedUser(t *testing.T) *model.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	require.NoError(t, err)
	return &model.User{ID: 42, Username: testUsername, Email: testEmail, PasswordHash: string(hash)}
}

// TestRegister_Validation 覆盖参数校验分支。mock 没有录制任何期望——
// 如果校验失败仍然触达了 repository，mockery 会让测试失败，
// 这行注释本身就是断言：「非法输入不应该触达数据库」。
func TestRegister_Validation(t *testing.T) {
	tests := []struct {
		name string
		req  *userpb.RegisterRequest
	}{
		{"缺少用户名", &userpb.RegisterRequest{Username: "", Email: testEmail, Password: testPassword}},
		{"缺少邮箱", &userpb.RegisterRequest{Username: testUsername, Email: "", Password: testPassword}},
		{"密码太短", &userpb.RegisterRequest{Username: testUsername, Email: testEmail, Password: "12345"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTestService(t) // mock 零期望：任何 repo 调用都会失败

			resp, err := svc.Register(context.Background(), tt.req)

			require.Error(t, err)
			assert.Nil(t, resp)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

func TestRegister_Success(t *testing.T) {
	svc, repo := newTestService(t)

	var captured *model.User
	repo.EXPECT().Create(mock.Anything, mock.Anything).
		Run(func(_ context.Context, u *model.User) {
			captured = u
			u.ID = 42 // 模拟 GORM 的行为：Create 后回填自增主键
		}).
		Return(nil)

	resp, err := svc.Register(context.Background(), &userpb.RegisterRequest{
		Username: testUsername, Email: testEmail, Password: testPassword,
	})

	require.NoError(t, err)
	require.NotNil(t, resp.GetUser())

	// 密码必须是哈希后的：既不等于明文，又能被 bcrypt 校验通过。
	assert.NotEqual(t, testPassword, captured.PasswordHash)
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(captured.PasswordHash), []byte(testPassword)))

	assert.Equal(t, uint64(42), resp.GetUser().GetId())
	assert.Equal(t, testUsername, resp.GetUser().GetUsername())
	assert.Equal(t, testEmail, resp.GetUser().GetEmail())
}

// TestRegister_DuplicateUsername 固化「唯一键冲突 -> 409」的翻译链路：
// repository 把冲突翻译成 AppError(AlreadyExists)，service 只负责透传
// （ToGRPCStatus）。这里 mock 直接返回翻译后的错误，验证的是 service
// 不破坏这个语义。
func TestRegister_DuplicateUsername(t *testing.T) {
	svc, repo := newTestService(t)
	repo.EXPECT().Create(mock.Anything, mock.Anything).
		Return(apperrors.AlreadyExists("username or email already exists", nil))

	resp, err := svc.Register(context.Background(), &userpb.RegisterRequest{
		Username: testUsername, Email: testEmail, Password: testPassword,
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

func TestRegister_RepoInternalError(t *testing.T) {
	svc, repo := newTestService(t)
	repo.EXPECT().Create(mock.Anything, mock.Anything).
		Return(apperrors.Internal("failed to create user", nil))

	resp, err := svc.Register(context.Background(), &userpb.RegisterRequest{
		Username: testUsername, Email: testEmail, Password: testPassword,
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// TestLogin_AntiEnumeration 是本文件最重要的一组用例：「用户不存在」和
// 「密码错误」对外必须返回完全相同的 gRPC code 和 message。一旦有人把
// 两种失败改出差异（比如分开提示"用户不存在"），这条测试立刻变红——
// 防用户枚举的设计由此被测试固化，而不是停留在注释里。
func TestLogin_AntiEnumeration(t *testing.T) {
	tests := []struct {
		name      string
		username  string
		password  string
		setupRepo func(repo *mocks.MockRepository)
	}{
		{
			name:     "用户不存在",
			username: "nobody",
			password: testPassword,
			setupRepo: func(repo *mocks.MockRepository) {
				repo.EXPECT().GetByUsername(mock.Anything, "nobody").
					Return(nil, apperrors.NotFound("user not found", nil))
			},
		},
		{
			name:     "密码错误",
			username: testUsername,
			password: "wrong-pass",
			setupRepo: func(repo *mocks.MockRepository) {
				repo.EXPECT().GetByUsername(mock.Anything, testUsername).
					Return(storedUser(t), nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo := newTestService(t)
			tt.setupRepo(repo)

			_, err := svc.Login(context.Background(), &userpb.LoginRequest{
				Username: tt.username, Password: tt.password,
			})

			require.Error(t, err)
			assert.Equal(t, codes.Unauthenticated, status.Code(err))
			assert.Equal(t, "invalid username or password", status.Convert(err).Message())
		})
	}
}

func TestLogin_Success(t *testing.T) {
	svc, repo := newTestService(t)
	repo.EXPECT().GetByUsername(mock.Anything, testUsername).
		Return(storedUser(t), nil)

	resp, err := svc.Login(context.Background(), &userpb.LoginRequest{
		Username: testUsername, Password: testPassword,
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.GetToken())

	// token 不是只看非空就完事：parse 回来验证 claims，确认签发时
	// 用的是正确的 secret 和用户身份——这才闭环。
	claims, err := appjwt.Parse(resp.GetToken(), testJWTSecret)
	require.NoError(t, err)
	assert.Equal(t, uint64(42), claims.UserID)
	assert.Equal(t, testUsername, claims.Username)

	// 返回的 user 结构里没有 PasswordHash 字段——这是 proto 类型系统
	// 保证的（model.User 有、proto.User 没有），这里顺手验证转换没抄错字段。
	assert.Equal(t, testUsername, resp.GetUser().GetUsername())
}

func TestGetUser_NotFound(t *testing.T) {
	svc, repo := newTestService(t)
	repo.EXPECT().GetByID(mock.Anything, uint64(999)).
		Return(nil, apperrors.NotFound("user not found", nil))

	resp, err := svc.GetUser(context.Background(), &userpb.GetUserRequest{Id: 999})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestGetUser_Success(t *testing.T) {
	svc, repo := newTestService(t)
	repo.EXPECT().GetByID(mock.Anything, uint64(42)).
		Return(storedUser(t), nil)

	resp, err := svc.GetUser(context.Background(), &userpb.GetUserRequest{Id: 42})

	require.NoError(t, err)
	assert.Equal(t, uint64(42), resp.GetUser().GetId())
	assert.Equal(t, testEmail, resp.GetUser().GetEmail())
}
