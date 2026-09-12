import { Empty } from 'antd';
import { useSearchParams } from 'react-router-dom';

// 登录（commit 10 实现）：成功写 session store → 跳 ?redirect= 或 /。
export default function Login() {
  const [searchParams] = useSearchParams();
  const redirect = searchParams.get('redirect') || '/';
  return <Empty description={`登录见 commit 10，成功后将跳回 ${redirect}`} />;
}
