// Package router 负责把 URL 路径映射到 handler 方法，是唯一知道具体路由表的地方。
// main.go 只需要调用 router.New(...) 拿到一个 *gin.Engine，不需要关心路由细节。
package router

import (
	"github.com/gin-gonic/gin"

	"go-ecom-admin/internal/gateway/handler"
	"go-ecom-admin/internal/gateway/middleware"
)

// New 需要 jwtSecret 才能组装出鉴权中间件——路由表是唯一决定"哪些接口
// 需要登录"的地方，所以中间件的接入点也放在这里，而不是让每个 handler
// 自己判断要不要校验 token。
func New(h *handler.Handler, jwtSecret string) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	auth := middleware.Auth(jwtSecret)

	api := r.Group("/api/v1")
	{
		// /auth 下的接口本身就是"证明你是谁"的入口，不能反过来要求已经登录。
		authGroup := api.Group("/auth")
		authGroup.POST("/register", h.Register)
		authGroup.POST("/login", h.Login)

		users := api.Group("/users")
		users.GET("/:id", h.GetUser)

		// 商品的"读"保持公开（游客可浏览），"写"（新增/改/删）需要登录——
		// 这是本阶段唯一的权限模型，还没有细到"哪个用户能改哪个商品"。
		products := api.Group("/products")
		products.GET("/:id", h.GetProduct)
		products.GET("", h.ListProducts)
		products.POST("", auth, h.CreateProduct)
		products.PUT("/:id", auth, h.UpdateProduct)
		products.DELETE("/:id", auth, h.DeleteProduct)
	}

	return r
}
