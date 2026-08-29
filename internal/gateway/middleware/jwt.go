// Package middleware 存放 Gin 中间件。目前只有一个 JWT 鉴权中间件，
// 之所以单独开一个包而不是塞进 handler，是因为中间件是"横切关注点"——
// 它不属于任何一个具体资源（user/product），却要作用在多条路由上，
// 放在 router 组装路由的地方直接引用会更清楚。
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"go-ecom-admin/pkg/jwt"
)

// gin.Context.Set/Get 操作的是一个按请求隔离的 map[string]any，和标准库
// context.Context 需要用不可比较的自定义类型防碰撞不是一回事——这里用
// 导出的字符串常量就够了，也方便 handler 层直接引用同一个 key。
const (
	userIDKey   = "user_id"
	usernameKey = "username"
)

// Auth 返回一个只做一件事的中间件：校验 Authorization header 里的 JWT，
// 通过则把 userID/username 写进 gin.Context 供后续 handler 读取，不通过
// 直接 401 并 Abort（不再执行后面的 handler）。
//
// secret 在 router 组装路由时以参数形式传入（来自 cfg.JWT.Secret），
// 这里不读全局配置——和 pkg/jwt 的设计保持一致，谁用哪个 secret 一目了然。
func Auth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		const prefix = "Bearer "
		if header == "" || !strings.HasPrefix(header, prefix) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or malformed authorization header"})
			return
		}

		tokenString := strings.TrimPrefix(header, prefix)
		claims, err := jwt.Parse(tokenString, secret)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		c.Set(userIDKey, claims.UserID)
		c.Set(usernameKey, claims.Username)
		c.Next()
	}
}

// UserIDFromContext 供以后需要知道"当前登录用户是谁"的 handler 使用
// （例如"只能删除自己发布的商品"这类归属校验，阶段 2 暂时用不上，但
// 中间件已经把数据放进 context 了，先把取数据的入口定义好）。
func UserIDFromContext(c *gin.Context) (uint64, bool) {
	v, ok := c.Get(userIDKey)
	if !ok {
		return 0, false
	}
	id, ok := v.(uint64)
	return id, ok
}

func UsernameFromContext(c *gin.Context) (string, bool) {
	v, ok := c.Get(usernameKey)
	if !ok {
		return "", false
	}
	name, ok := v.(string)
	return name, ok
}
