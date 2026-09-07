// Package router 负责把 URL 路径映射到 handler 方法，是唯一知道具体路由表的地方。
// main.go 只需要调用 router.New(...) 拿到一个 *gin.Engine，不需要关心路由细节。
package router

import (
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"go-ecom-admin/internal/gateway/handler"
	"go-ecom-admin/internal/gateway/middleware"
)

// 限流参数：每 IP 每秒补 10 个令牌，桶容量 20（允许 20 个请求的瞬时突发）。
// 取值思路：正常浏览商品的频率远低于 10 QPS，这个额度对真实用户无感，
// 但能挡住脚本对 /auth/login 这类重接口（bcrypt 校验约 100ms/次）的爆破。
// 学习项目先用常量；生产上应该进 config，配合多副本还要换 Redis 分布式限流
// （理由见 ratelimit.go 里 ipRateLimiter 的注释）。
const (
	rateLimitPerSecond rate.Limit = 10
	rateLimitBurst                = 20
)

// New 需要 jwtSecret 才能组装出鉴权中间件——路由表是唯一决定"哪些接口
// 需要登录"的地方，所以中间件的接入点也放在这里，而不是让每个 handler
// 自己判断要不要校验 token。
func New(h *handler.Handler, jwtSecret string) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	// 限流是所有中间件里最外层的一道：比 JWT 验签更早执行才省钱，
	// 且未登录接口（/auth/login）也受它保护。
	r.Use(middleware.RateLimit(rateLimitPerSecond, rateLimitBurst))

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
