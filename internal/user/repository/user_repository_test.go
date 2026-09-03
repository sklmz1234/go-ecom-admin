// user 服务 repository 层单元测试：用 sqlite :memory: 跑真实 GORM 逻辑。
//
// 为什么这里不用 mock？因为要验证的恰恰是「我对 GORM 行为的假设」——
// First 查不到返回 gorm.ErrRecordNotFound（而不是 nil）、错误翻译成
// 业务 AppError 是否正确。mock 一个 GORM 只是在测试我自己写的假设。
//
// 方言边界：sqlite 只负责方言无关的 GORM 行为；唯一键冲突翻译
// （依赖 MySQL 驱动把 1062 翻译成 gorm.ErrDuplicatedKey）是方言敏感的，
// 放在 user_repository_mysql_test.go（build tag: integration）里。
package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	apperrors "go-ecom-admin/pkg/errors"

	"go-ecom-admin/internal/user/model"
)

// newSQLiteDB 每个测试拿到一个独立的内存库：测试之间零共享状态，
// 也不需要清理逻辑（库随测试结束消失）。
func newSQLiteDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{TranslateError: true})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	return db
}

// requireAppCode 断言错误是一条指定 Code 的 AppError——repository 的
// 契约是「永远返回翻译后的业务错误，不把 GORM/驱动错误漏出去」。
func requireAppCode(t *testing.T, err error, want apperrors.Code) {
	t.Helper()
	require.Error(t, err)
	var appErr *apperrors.AppError
	require.True(t, errors.As(err, &appErr), "应该返回 *AppError，实际是 %T: %v", err, err)
	assert.Equal(t, want, appErr.Code)
}

func TestCreate_Success(t *testing.T) {
	repo := NewGormRepository(newSQLiteDB(t))

	u := &model.User{Username: "sklmz", Email: "sklmz@example.com", PasswordHash: "hash"}
	require.NoError(t, repo.Create(context.Background(), u))

	// 自增主键由 GORM 在 Create 后回填——不回填说明主键约定被破坏。
	assert.NotZero(t, u.ID)
}

func TestGetByID_NotFound(t *testing.T) {
	repo := NewGormRepository(newSQLiteDB(t))

	u, err := repo.GetByID(context.Background(), 999)

	require.Nil(t, u)
	requireAppCode(t, err, apperrors.CodeNotFound)
}

func TestGetByUsername(t *testing.T) {
	repo := NewGormRepository(newSQLiteDB(t))
	seed := &model.User{Username: "sklmz", Email: "sklmz@example.com", PasswordHash: "hash"}
	require.NoError(t, repo.Create(context.Background(), seed))

	t.Run("命中", func(t *testing.T) {
		u, err := repo.GetByUsername(context.Background(), "sklmz")
		require.NoError(t, err)
		require.NotNil(t, u)
		assert.Equal(t, seed.ID, u.ID)
		assert.Equal(t, "sklmz@example.com", u.Email)
	})

	t.Run("未命中翻译成业务NotFound", func(t *testing.T) {
		u, err := repo.GetByUsername(context.Background(), "nobody")
		require.Nil(t, u)
		requireAppCode(t, err, apperrors.CodeNotFound)
	})
}
