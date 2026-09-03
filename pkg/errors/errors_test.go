// pkg/errors 的转换表测试：AppError.Code -> gRPC code -> HTTP status
// 是全项目错误语义的「宪法」，任何映射改动（比如把 AlreadyExists 从
// 409 改成 400）都必须先过这张表。
package errors_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apperrors "go-ecom-admin/pkg/errors"
)

func TestToGRPCStatus(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode codes.Code
		wantMsg  string
	}{
		{"nil透传", nil, codes.OK, ""},
		{"NotFound", apperrors.NotFound("user not found", nil), codes.NotFound, "user not found"},
		{"InvalidArgument", apperrors.InvalidArgument("bad request", nil), codes.InvalidArgument, "bad request"},
		{"AlreadyExists", apperrors.AlreadyExists("already exists", nil), codes.AlreadyExists, "already exists"},
		{"Unauthorized", apperrors.Unauthorized("invalid username or password", nil), codes.Unauthenticated, "invalid username or password"},

		// Internal 走 default 分支：message 被替换成 "internal error"，
		// 底层细节（"boom"）不透传给客户端——这是有意的防泄露设计。
		{"Internal细节不泄露", apperrors.Internal("boom", nil), codes.Internal, "internal error"},

		// 非业务错误（驱动报错、panic 恢复等）统一 500 且不泄露细节。
		{"未知错误收敛为Internal", errors.New("sql: connection refused"), codes.Internal, "internal error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := apperrors.ToGRPCStatus(tt.err)
			assert.Equal(t, tt.wantCode, status.Code(got))
			if got != nil {
				assert.Equal(t, tt.wantMsg, status.Convert(got).Message())
			}
		})
	}
}

// TestToGRPCStatus_Wrapped 验证包装过的 AppError（fmt.Errorf("%w") 包装）
// 依然能被 errors.As 解出——这是把 AppError 设计成实现 Unwrap 的原因。
func TestToGRPCStatus_Wrapped(t *testing.T) {
	wrapped := wrap(apperrors.NotFound("user not found", nil))

	got := apperrors.ToGRPCStatus(wrapped)

	assert.Equal(t, codes.NotFound, status.Code(got))
}

func wrap(err error) error { return errors.Join(errors.New("outer"), err) }

func TestToHTTPStatus(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantMsg  string
	}{
		{"gRPCNotFound映射404", status.Error(codes.NotFound, "user not found"), http.StatusNotFound, "user not found"},
		{"gRPCInvalidArgument映射400", status.Error(codes.InvalidArgument, "bad request"), http.StatusBadRequest, "bad request"},
		{"gRPCAlreadyExists映射409", status.Error(codes.AlreadyExists, "conflict"), http.StatusConflict, "conflict"},
		{"gRPCUnauthenticated映射401", status.Error(codes.Unauthenticated, "no token"), http.StatusUnauthorized, "no token"},
		{"非gRPC错误映射500", errors.New("boom"), http.StatusInternalServerError, "internal error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, msg := apperrors.ToHTTPStatus(tt.err)
			assert.Equal(t, tt.wantCode, code)
			assert.Equal(t, tt.wantMsg, msg)
		})
	}
}
