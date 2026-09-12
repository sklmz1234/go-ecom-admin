import { Layout, Input, Button, Dropdown, Space } from 'antd';
import { Link, Outlet, useNavigate, useSearchParams } from 'react-router-dom';
import { useSessionStore } from '../stores/session';

const { Header, Content, Footer } = Layout;

// 顶栏：logo / 搜索框 / 登录态。
// 搜索框不持有结果，只负责把 keyword 写进首页的 URL query（/?keyword=xxx），
// 真正的查询由首页根据 URL 发起——keyword 进 URL 才能保证刷新/分享后结果一致。
export default function AppLayout() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const keyword = searchParams.get('keyword') || '';

  const token = useSessionStore((s) => s.token);
  const user = useSessionStore((s) => s.user);
  const clearSession = useSessionStore((s) => s.clearSession);

  const handleSearch = (value) => {
    const kw = value.trim();
    navigate(kw ? `/?keyword=${encodeURIComponent(kw)}` : '/');
  };

  const handleLogout = () => {
    clearSession();
    navigate('/');
  };

  const userMenu = {
    items: [
      { key: 'orders', label: '我的订单' },
      { key: 'logout', label: '退出登录', danger: true },
    ],
    onClick: ({ key }) => {
      if (key === 'orders') navigate('/orders');
      if (key === 'logout') handleLogout();
    },
  };

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Header style={{ display: 'flex', alignItems: 'center', gap: 24 }}>
        <Link to="/" style={{ color: '#fff', fontSize: 18, fontWeight: 600, whiteSpace: 'nowrap' }}>
          GoEcom 商城
        </Link>
        {/* key=keyword：URL 里的关键词变化时强制重建输入框，保证框内文字与 URL 同步 */}
        <Input.Search
          key={keyword}
          defaultValue={keyword}
          placeholder="搜索商品"
          allowClear
          onSearch={handleSearch}
          style={{ maxWidth: 420 }}
        />
        <div style={{ marginLeft: 'auto' }}>
          {token ? (
            <Dropdown menu={userMenu}>
              <Space style={{ color: '#fff', cursor: 'pointer' }}>{user?.username}</Space>
            </Dropdown>
          ) : (
            <Space>
              <Button type="link" style={{ color: '#fff' }} onClick={() => navigate('/login')}>
                登录
              </Button>
              <Button ghost onClick={() => navigate('/register')}>
                注册
              </Button>
            </Space>
          )}
        </div>
      </Header>
      <Content style={{ padding: '24px', maxWidth: 1200, width: '100%', margin: '0 auto' }}>
        <Outlet />
      </Content>
      <Footer style={{ textAlign: 'center', color: '#999' }}>
        GoEcom 商城 · 学习项目
      </Footer>
    </Layout>
  );
}
