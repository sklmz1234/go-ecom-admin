// Package errors 定义跨分层、跨传输协议（gRPC / HTTP）复用的业务错误类型。
//
// 设计决策：service/repository 层只关心「发生了什么业务错误」（找不到记录、参数非法……），
// 不应该关心自己最终会被 gRPC server 还是 Gin handler 调用。所以业务错误码
// 和 gRPC codes.Code / HTTP status 是分离的，转换只发生在最外层（gateway 的
// handler、user/product-service 的 grpc server 入口）。
package errors

import (
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Code 是与传输协议无关的业务错误码。
type Code int

const (
	CodeUnknown Code = iota
	CodeNotFound
	CodeInvalidArgument
	CodeAlreadyExists
	CodeInternal
)

// AppError 是本项目统一的错误载体：Code 供上层做分支判断，
// Message 是可以直接展示给客户端的文案，Err 保留底层原始错误用于日志排查
// （但不会经由 gRPC/HTTP 返回给客户端，避免泄露内部实现细节，例如 SQL 报错信息）。
type AppError struct {
	Code    Code
	Message string
	Err     error
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *AppError) Unwrap() error {
	return e.Err
}

func New(code Code, message string, cause error) *AppError {
	return &AppError{Code: code, Message: message, Err: cause}
}

func NotFound(message string, cause error) *AppError {
	return New(CodeNotFound, message, cause)
}

func InvalidArgument(message string, cause error) *AppError {
	return New(CodeInvalidArgument, message, cause)
}

func AlreadyExists(message string, cause error) *AppError {
	return New(CodeAlreadyExists, message, cause)
}

func Internal(message string, cause error) *AppError {
	return New(CodeInternal, message, cause)
}

// ToGRPCStatus 供 user-service / product-service 的 gRPC handler 使用，
// 把内部 AppError 转换成客户端能识别的标准 gRPC status。
func ToGRPCStatus(err error) error {
	if err == nil {
		return nil
	}

	var appErr *AppError
	if !errors.As(err, &appErr) {
		return status.Error(codes.Internal, "internal error")
	}

	switch appErr.Code {
	case CodeNotFound:
		return status.Error(codes.NotFound, appErr.Message)
	case CodeInvalidArgument:
		return status.Error(codes.InvalidArgument, appErr.Message)
	case CodeAlreadyExists:
		return status.Error(codes.AlreadyExists, appErr.Message)
	default:
		// 不把 appErr.Err（可能包含 SQL 语句/驱动报错）透传给客户端，
		// 完整信息已经在 service 层落日志，这里只暴露安全的提示文案。
		return status.Error(codes.Internal, "internal error")
	}
}

// ToHTTPStatus 供 api-gateway 的 Gin handler 使用。gateway 从下游 gRPC 服务
// 收到的是 gRPC status error，所以这里按 gRPC code 而不是 AppError.Code 判断——
// gateway 从不直接产生 AppError，它只是转译下游的错误。
func ToHTTPStatus(err error) (int, string) {
	st, ok := status.FromError(err)
	if !ok {
		return http.StatusInternalServerError, "internal error"
	}

	switch st.Code() {
	case codes.NotFound:
		return http.StatusNotFound, st.Message()
	case codes.InvalidArgument:
		return http.StatusBadRequest, st.Message()
	case codes.AlreadyExists:
		return http.StatusConflict, st.Message()
	case codes.OK:
		return http.StatusOK, ""
	default:
		return http.StatusInternalServerError, "internal error"
	}
}
