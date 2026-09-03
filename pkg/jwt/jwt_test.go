// pkg/jwt 的往返测试：Generate -> Parse 闭环，外加过期与签名错误两个
// 负例。secret 是显式参数（不是全局状态）的设计让这里不需要任何
// setup/teardown——每个用例想用什么 secret 就传什么。
package jwt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appjwt "go-ecom-admin/pkg/jwt"
)

func TestGenerateAndParse(t *testing.T) {
	token, err := appjwt.Generate(42, "sklmz", "test-secret", 1)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := appjwt.Parse(token, "test-secret")
	require.NoError(t, err)
	assert.Equal(t, uint64(42), claims.UserID)
	assert.Equal(t, "sklmz", claims.Username)
}

func TestParse_WrongSecret(t *testing.T) {
	token, err := appjwt.Generate(42, "sklmz", "test-secret", 1)
	require.NoError(t, err)

	_, err = appjwt.Parse(token, "other-secret")
	require.Error(t, err) // 签名不匹配必须失败，而不是返回空 claims
}

func TestParse_ExpiredToken(t *testing.T) {
	// expireHours 传 -1：签发即过期。jwt 库在 Parse 时自动校验 exp。
	token, err := appjwt.Generate(42, "sklmz", "test-secret", -1)
	require.NoError(t, err)

	_, err = appjwt.Parse(token, "test-secret")
	require.Error(t, err)
}
