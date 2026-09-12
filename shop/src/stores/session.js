import { create } from 'zustand';
import { persist } from 'zustand/middleware';

// 会话存 localStorage（zustand persist 的默认存储），关浏览器重开仍是登录态——
// C 端用户预期和管理台不同：管理台 frontend/ 用 sessionStorage 是"关窗即登出"
// 的运维姿态，商城用户没人想每次打开都重新登录。
export const useSessionStore = create(
  persist(
    (set) => ({
      token: null,
      user: null,
      // 登录成功时一次性写入 {token, user}，两个字段同生共死，
      // 不提供单独改 user 的 action，避免出现"有 token 没 user"的中间态。
      setSession: ({ token, user }) => set({ token, user }),
      clearSession: () => set({ token: null, user: null }),
    }),
    { name: 'go-ecom-shop.session' },
  ),
);
