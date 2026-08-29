// Package jwt 封装 JWT 的签发与校验。
//
// 设计决策：Generate/Parse 都把 secret 作为显式参数传入，而不是包级全局变量
// 或者自己去读 Viper——这样这个包完全不知道配置从哪来（和 pkg/logger 的风格
// 一致：main.go 负责把配置值取出来，逐个传给需要它的包）。好处是没有隐藏的
// 全局可变状态，调用方是谁、用的哪个 secret 一目了然，测试时也可以随便传
// 一个假 secret 而不用动全局配置。
package jwt

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims 是写进 JWT payload 的自定义字段，嵌入 RegisteredClaims 拿到标准的
// exp/iat 支持（过期校验由 jwt 库自动完成，不用自己比较时间戳）。
type Claims struct {
	UserID   uint64 `json:"user_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// Generate 签发一个 HS256 签名的 JWT，expireHours 小时后过期。
func Generate(userID uint64, username, secret string, expireHours int) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(expireHours) * time.Hour)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("jwt: sign token: %w", err)
	}
	return signed, nil
}

// Parse 校验并解析一个 JWT，返回其中的 Claims。
func Parse(tokenString, secret string) (*Claims, error) {
	claims := &Claims{}

	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		// 必须显式检查签名算法是不是 HMAC 族，否则如果服务端某处误用了
		// "认 token header 里声明的 alg" 这种写法，攻击者可以把 alg 改成
		// none 或者用公钥当 HMAC secret 伪造 token（经典的 alg-confusion
		// 攻击）。这里的 secret 只对 HMAC 有意义，遇到别的算法直接拒绝。
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("jwt: parse token: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("jwt: invalid token")
	}

	return claims, nil
}
