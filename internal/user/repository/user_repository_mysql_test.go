//go:build integration

// 方言敏感用例：唯一键冲突翻译。
//
// 为什么单独放这个文件（而不是上面的 sqlite 套件）：
// gorm.ErrDuplicatedKey 依赖「驱动把方言错误翻译成 GORM 统一错误」——
// MySQL 是 1062 错误码，sqlite 是另一种报错文本，PostgreSQL 又不一样。
// repository.Create 里 errors.Is(err, gorm.ErrDuplicatedKey) 的行为
// 只有在目标方言（生产用的 MySQL）上验证才有意义。
//
// 运行方式（默认 go test 不编译本文件）：
//
//	TEST_MYSQL_DSN="root:root@tcp(127.0.0.1:3306)/ecom_test?parseTime=true" \
//	  go test -tags=integration ./internal/user/repository/ -run TestCreate_Duplicate -v
package repository

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	apperrors "go-ecom-admin/pkg/errors"

	"go-ecom-admin/internal/user/model"
)

func newMySQLDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未设置 TEST_MYSQL_DSN，跳过 MySQL 方言用例（本地起一个 MySQL 后再跑）")
	}

	// TranslateError 必须和 cmd/user-service/main.go 保持一致——这个
	// 测试同时是那行配置的回归验证：谁把它删了，这里立刻变红。
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{TranslateError: true})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	return db
}

func TestCreate_DuplicateUsername(t *testing.T) {
	db := newMySQLDB(t)
	repo := NewGormRepository(db)

	// 用户名带纳秒时间戳，避免和历史数据/上一次运行冲突，无需清理。
	suffix := time.Now().UnixNano()
	existing := &model.User{
		Username:     fmt.Sprintf("dup_user_%d", suffix),
		Email:        fmt.Sprintf("dup_user_%d@example.com", suffix),
		PasswordHash: "hash",
	}
	require.NoError(t, repo.Create(context.Background(), existing))

	t.Run("重复用户名返回409语义", func(t *testing.T) {
		conflict := &model.User{
			Username:     existing.Username, // 用户名撞车，邮箱换一个，定位唯一冲突源
			Email:        fmt.Sprintf("other_%d@example.com", suffix),
			PasswordHash: "hash",
		}
		err := repo.Create(context.Background(), conflict)
		requireAppCode(t, err, apperrors.CodeAlreadyExists)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("重复邮箱同样返回409语义", func(t *testing.T) {
		conflict := &model.User{
			Username:     fmt.Sprintf("dup_user2_%d", suffix),
			Email:        existing.Email, // 这次撞邮箱
			PasswordHash: "hash",
		}
		err := repo.Create(context.Background(), conflict)
		requireAppCode(t, err, apperrors.CodeAlreadyExists)
	})
}
