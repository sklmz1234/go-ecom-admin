// Package router 负责把 URL 路径映射到 handler 方法，是唯一知道具体路由表的地方。
// main.go 只需要调用 router.New(...) 拿到一个 *gin.Engine，不需要关心路由细节。
package router

import (
	"github.com/gin-gonic/gin"

	"go-ecom-admin/internal/gateway/handler"
)

func New(h *handler.Handler) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	api := r.Group("/api/v1")
	{
		users := api.Group("/users")
		users.GET("/:id", h.GetUser)
		users.POST("", h.CreateUser)

		products := api.Group("/products")
		products.GET("/:id", h.GetProduct)
		products.POST("", h.CreateProduct)
		products.GET("", h.ListProducts)
	}

	return r
}
