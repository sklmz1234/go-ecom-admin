import { request } from './request';

// 接口契约全部以网关真实代码为准：
//   路由  internal/gateway/router/router.go
//   DTO   internal/gateway/model/dto.go
// 注意：价格字段网关已换算成元（price_yuan / total_yuan / unit_price_yuan），
// 前端只做 toFixed(2) 展示，不再做分→元换算。

// —— 认证（公开）——

// POST /api/v1/auth/login → {token, user:{id,username,email,created_at}}
export function login(credentials) {
  return request('/api/v1/auth/login', { method: 'POST', body: credentials });
}

// POST /api/v1/auth/register → user（注册不返 token，成功后要跳登录页）
export function register(account) {
  return request('/api/v1/auth/register', { method: 'POST', body: account });
}

// —— 商品（公开读）——

// GET /api/v1/products?keyword=&page=&page_size= → {products:[...], total}
// keyword 空串 = 普通分页列表；非空 = product-service 内部走 ES 召回，网关无感知。
// pageSize 默认 12：首页卡片网格 4 列 × 3 行，比管理台的 10 更适合 C 端浏览。
export function listProducts({ keyword = '', page = 1, pageSize = 12 } = {}) {
  const query = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (keyword) query.set('keyword', keyword);
  return request(`/api/v1/products?${query}`);
}

// GET /api/v1/products/:id → ProductDTO（含 description/image_url）
export function getProduct(id) {
  return request(`/api/v1/products/${encodeURIComponent(id)}`);
}

// —— 订单（全部需要登录）——

// GET /api/v1/orders?page= → {orders:[...], total}
// 列表不带 items（omitempty），明细必须调 getOrder。
export function listMyOrders({ page = 1, pageSize = 10 } = {}) {
  const query = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  return request(`/api/v1/orders?${query}`, { requiresAuth: true });
}

// GET /api/v1/orders/:id → OrderDTO（唯一带 items 的接口）
export function getOrder(id) {
  return request(`/api/v1/orders/${encodeURIComponent(id)}`, { requiresAuth: true });
}

// POST /api/v1/orders body {items:[{product_id, quantity}]} → 201 OrderDTO
// 请求里没有价格字段——价格快照由 product-service 扣减时刻给出，客户端报价不可信。
// 409 = 库存不足，错误消息可直接展示。
export function createOrder(items) {
  return request('/api/v1/orders', { method: 'POST', body: { items }, requiresAuth: true });
}

// POST /api/v1/orders/:id/cancel → 更新后的 OrderDTO（4A outbox 异步回补库存）
export function cancelOrder(id) {
  return request(`/api/v1/orders/${encodeURIComponent(id)}/cancel`, {
    method: 'POST',
    requiresAuth: true,
  });
}
