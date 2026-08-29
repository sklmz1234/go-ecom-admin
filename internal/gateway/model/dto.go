// Package model 定义 api-gateway 对外的 REST DTO（JSON 请求/响应结构体）。
//
// 设计决策：故意不直接把 proto 生成的类型（userpb.User 等）当作 JSON 响应体返回。
// REST 客户端（前端/第三方）看到的字段名、字段形态应该由 gateway 自己控制，
// 不应该随下游 gRPC 服务的 proto 变动而被动改变——这是一层反腐层（anti-corruption layer）。
package model

// UserDTO 是暴露给 REST 客户端的用户视图。
type UserDTO struct {
	ID        uint64 `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	CreatedAt int64  `json:"created_at"`
}

// RegisterRequest 对应 POST /api/v1/auth/register。密码只在这个 DTO 里
// 短暂存在（HTTP body -> service 转发给 user-service），gateway 自己
// 不做任何哈希/存储，那是 user-service 的职责。
type RegisterRequest struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginResponseDTO 比 UserDTO 多一个 token 字段——单独定义而不是给
// UserDTO 加可选 token 字段，避免 GetUser 之类的接口也"顺带"带上一个
// 永远是空字符串的 token 字段，语义不清楚。
type LoginResponseDTO struct {
	Token string   `json:"token"`
	User  *UserDTO `json:"user"`
}

// ProductDTO 是暴露给 REST 客户端的商品视图。
// 注意这里把内部的 PriceCents 换算成 PriceYuan 展示——传输/存储用分，
// 展示给最终用户用元，转换逻辑属于 gateway 的职责，不应该让下游服务操心。
type ProductDTO struct {
	ID        uint64  `json:"id"`
	Name      string  `json:"name"`
	PriceYuan float64 `json:"price_yuan"`
	Stock     int32   `json:"stock"`
	CreatedAt int64   `json:"created_at"`
}

type CreateProductRequest struct {
	Name      string  `json:"name" binding:"required"`
	PriceYuan float64 `json:"price_yuan" binding:"required,gt=0"`
	Stock     int32   `json:"stock"`
}

// UpdateProductRequest 和 CreateProductRequest 字段一样，但故意不复用
// 同一个结构体：语义不同（一个是"创建"一个是"整体替换"），以后两者的
// 校验规则很可能会分叉（比如 Update 可能要允许部分字段可选），现在分开
// 定义能省掉以后拆分时的破坏性改动。
type UpdateProductRequest struct {
	Name      string  `json:"name" binding:"required"`
	PriceYuan float64 `json:"price_yuan" binding:"required,gt=0"`
	Stock     int32   `json:"stock"`
}

type ListProductsRequest struct {
	Page     int `form:"page,default=1"`
	PageSize int `form:"page_size,default=20"`
}

type ListProductsResponse struct {
	Products []*ProductDTO `json:"products"`
	Total    int64         `json:"total"`
}
