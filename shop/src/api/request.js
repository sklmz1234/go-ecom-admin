import { useSessionStore } from '../stores/session';

// 与管理台 AuthError 同语义：调用方可以用 err.isAuthError 区分"登录失效"
// 和普通业务错误（比如 409 库存不足），避免把登录失效也弹成红色错误提示。
export class AuthError extends Error {
  constructor(message = '登录已失效，请重新登录。') {
    super(message);
    this.name = 'AuthError';
    this.isAuthError = true;
  }
}

// request 是所有接口的唯一出口，思路抄 frontend/src/api.js，两处 React 化改动：
// 1. token 来源从 sessionStorage 换成 zustand store（getState() 可在组件外读）；
// 2. 401 的处理从"派发 autherror 事件"换成直接跳转 /login?redirect=<当前路径>。
//    request.js 在 React 树外拿不到 router 的 navigate，用整页跳转兜底——
//    登录过期本就是"推倒重来"的场景，丢一点 SPA 内存状态无所谓。
export async function request(path, { method = 'GET', body, requiresAuth = false } = {}) {
  const headers = { Accept: 'application/json' };
  const { token, clearSession } = useSessionStore.getState();

  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (requiresAuth && token) headers.Authorization = `Bearer ${token}`;

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
    if (requiresAuth && response.status === 401) {
      clearSession();
      const redirect = window.location.pathname + window.location.search;
      window.location.assign(`/login?redirect=${encodeURIComponent(redirect)}`);
      throw new AuthError();
    }
    // 后端错误体统一是 {"error": "..."}（handler.respondGRPCError），
    // 原样抛给页面，由页面 message.error() 展示——409 库存不足必须可读。
    throw new Error(data.error || `请求失败（${response.status}）`);
  }

  return data;
}
