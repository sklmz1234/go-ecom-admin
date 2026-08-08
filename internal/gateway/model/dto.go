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

type CreateUserRequest struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
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

type ListProductsRequest struct {
	Page     int `form:"page,default=1"`
	PageSize int `form:"page_size,default=20"`
}

type ListProductsResponse struct {
	Products []*ProductDTO `json:"products"`
	Total    int64         `json:"total"`
}
