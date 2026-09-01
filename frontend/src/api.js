import { clearSession, getSession } from './store.js';

export class AuthError extends Error {
  constructor(message = '登录已失效，请重新登录。') {
    super(message);
    this.name = 'AuthError';
    this.isAuthError = true;
  }
}

async function request(path, { method = 'GET', body, requiresAuth = false } = {}) {
  const headers = { Accept: 'application/json' };
  const session = getSession();

  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (requiresAuth && session?.token) headers.Authorization = `Bearer ${session.token}`;

  let response;
  try {
    response = await fetch(path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new Error('无法连接到服务，请检查后端是否已启动。');
  }

  if (response.status === 204) return undefined;

  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    // 写操作的未认证状态意味着本地令牌已不可用，必须立即撤销会话。
    if (requiresAuth && response.status === 401) {
      clearSession();
      // 事件使表单、删除等自行处理异常的异步操作仍能回到登录页。
      window.dispatchEvent(new Event('autherror'));
      throw new AuthError();
    }
    throw new Error(data.error || `请求失败（${response.status}）`);
  }

  return data;
}

export function login(credentials) {
  return request('/api/v1/auth/login', { method: 'POST', body: credentials });
}

export function register(account) {
  return request('/api/v1/auth/register', { method: 'POST', body: account });
}

export function listProducts({ page = 1, pageSize = 10 } = {}) {
  const query = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  return request(`/api/v1/products?${query}`);
}

export function getProduct(id) {
  return request(`/api/v1/products/${encodeURIComponent(id)}`);
}

export function createProduct(product) {
  return request('/api/v1/products', { method: 'POST', body: product, requiresAuth: true });
}

export function updateProduct(id, product) {
  return request(`/api/v1/products/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: product,
    requiresAuth: true,
  });
}

export function deleteProduct(id) {
  return request(`/api/v1/products/${encodeURIComponent(id)}`, { method: 'DELETE', requiresAuth: true });
}
