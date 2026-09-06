// Package handler 是 Gin 的 HTTP 处理函数层：只做参数绑定、调用 service、
// 把结果/错误翻译成 HTTP 响应，不包含任何业务逻辑。
package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	apperrors "go-ecom-admin/pkg/errors"

	"go-ecom-admin/internal/gateway/middleware"
	"go-ecom-admin/internal/gateway/model"
	"go-ecom-admin/internal/gateway/service"
)

type Handler struct {
	svc *service.Service
	log *zap.Logger
}

func New(svc *service.Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

func (h *Handler) GetUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	user, err := h.svc.GetUser(c.Request.Context(), id)
	if err != nil {
		h.respondGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, user)
}

func (h *Handler) Register(c *gin.Context) {
	var req model.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user, err := h.svc.Register(c.Request.Context(), req)
	if err != nil {
		h.respondGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, user)
}

func (h *Handler) Login(c *gin.Context) {
	var req model.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.svc.Login(c.Request.Context(), req)
	if err != nil {
		h.respondGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) GetProduct(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	product, err := h.svc.GetProduct(c.Request.Context(), id)
	if err != nil {
		h.respondGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, product)
}

func (h *Handler) CreateProduct(c *gin.Context) {
	var req model.CreateProductRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, ok := middleware.UserIDFromContext(c)
	if !ok {
		// 路由上挂了 Auth 中间件时这里永远走不到——这是一次防御性检查：
		// 万一以后有人把写路由挂到 auth 组外面，至少不会带着"零身份"调到下游。
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing caller identity"})
		return
	}

	product, err := h.svc.CreateProduct(c.Request.Context(), userID, req)
	if err != nil {
		h.respondGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, product)
}

func (h *Handler) ListProducts(c *gin.Context) {
	var req model.ListProductsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.svc.ListProducts(c.Request.Context(), req)
	if err != nil {
		h.respondGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) UpdateProduct(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req model.UpdateProductRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, ok := middleware.UserIDFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing caller identity"})
		return
	}

	product, err := h.svc.UpdateProduct(c.Request.Context(), userID, id, req)
	if err != nil {
		h.respondGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, product)
}

func (h *Handler) DeleteProduct(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	userID, ok := middleware.UserIDFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing caller identity"})
		return
	}

	if err := h.svc.DeleteProduct(c.Request.Context(), userID, id); err != nil {
		h.respondGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// respondGRPCError 把下游 gRPC 服务返回的 status error 翻译成 HTTP 状态码。
// 这是 pkg/errors.ToHTTPStatus 唯一的调用点——gateway 里所有 handler 共用同一套翻译规则。
func (h *Handler) respondGRPCError(c *gin.Context, err error) {
	status, msg := apperrors.ToHTTPStatus(err)
	if status == http.StatusInternalServerError {
		h.log.Error("downstream call failed", zap.Error(err), zap.String("path", c.FullPath()))
	}
	c.JSON(status, gin.H{"error": msg})
}
