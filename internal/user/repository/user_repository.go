// Package repository 负责 user 服务的数据访问，是唯一允许出现 GORM/SQL 细节的地方。
// service 层只依赖下面的 Repository 接口，不知道底层是 MySQL 还是别的存储，
// 这样单测 service 时可以直接注入一个内存假实现，不用起真实数据库。
package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	apperrors "go-ecom-admin/pkg/errors"

	"go-ecom-admin/internal/user/model"
)

// Repository 是 user 服务的数据访问接口。
type Repository interface {
	Create(ctx context.Context, u *model.User) error
	GetByID(ctx context.Context, id uint64) (*model.User, error)
}

type gormRepository struct {
	db *gorm.DB
}

// NewGormRepository 用一个已经建立好连接的 *gorm.DB 构造 Repository。
// 连接的建立、AutoMigrate 都在 main.go 里完成，repository 不负责生命周期管理。
func NewGormRepository(db *gorm.DB) Repository {
	return &gormRepository{db: db}
}

func (r *gormRepository) Create(ctx context.Context, u *model.User) error {
	if err := r.db.WithContext(ctx).Create(u).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return apperrors.AlreadyExists("username or email already exists", err)
		}
		return apperrors.Internal("failed to create user", err)
	}
	return nil
}

func (r *gormRepository) GetByID(ctx context.Context, id uint64) (*model.User, error) {
	var u model.User
	if err := r.db.WithContext(ctx).First(&u, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("user not found", err)
		}
		return nil, apperrors.Internal("failed to query user", err)
	}
	return &u, nil
}
