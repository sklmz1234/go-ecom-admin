import { Navigate, useLocation } from 'react-router-dom';
import { useSessionStore } from '../stores/session';

// 路由守卫：无 token → 跳 /login，并把当前完整路径（含 query）带进 redirect，
// 登录成功后原路跳回。典型场景：游客在详情页点"立即购买"。
export default function RequireAuth({ children }) {
  const token = useSessionStore((s) => s.token);
  const location = useLocation();

  if (!token) {
    const redirect = location.pathname + location.search;
    return <Navigate to={`/login?redirect=${encodeURIComponent(redirect)}`} replace />;
  }
  return children;
}
