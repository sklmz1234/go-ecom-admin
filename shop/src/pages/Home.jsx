import { useEffect, useState } from 'react';
import { App, Card, Col, Empty, Pagination, Row, Spin, Tag } from 'antd';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { listProducts } from '../api';
import { formatPrice } from '../utils/format';

// 4 列 × 3 行（UI 决策 1）。和后端默认值 20 故意不同——C 端卡片大，
// 一页 12 个是浏览体验最舒服的密度。
const PAGE_SIZE = 12;

// image_url 是外链，可能为空串（proto3 字段兜底）或加载失败，都要兜占位图。
function ProductCover({ product }) {
  const [broken, setBroken] = useState(false);
  if (!product.image_url || broken) {
    return (
      <div
        style={{
          height: 180,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          background: '#f0f0f0',
          color: '#999',
        }}
      >
        暂无图片
      </div>
    );
  }
  return (
    <img
      src={product.image_url}
      alt={product.name}
      style={{ height: 180, width: '100%', objectFit: 'cover', display: 'block' }}
      onError={() => setBroken(true)}
    />
  );
}

export default function Home() {
  // URL query 是搜索/分页状态的单一事实源：可刷新、可分享链接、浏览器后退可用。
  const [searchParams, setSearchParams] = useSearchParams();
  const keyword = searchParams.get('keyword') || '';
  const page = Math.max(1, Number(searchParams.get('page')) || 1);

  const [products, setProducts] = useState([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const navigate = useNavigate();
  const { message } = App.useApp();

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    listProducts({ keyword, page, pageSize: PAGE_SIZE })
      .then((data) => {
        if (cancelled) return;
        setProducts(data.products || []);
        setTotal(data.total || 0);
      })
      .catch((e) => !cancelled && message.error(e.message))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
    // message 是 antd App 上下文的稳定引用，不进依赖。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [keyword, page]);

  function onPageChange(nextPage) {
    const params = { page: String(nextPage) };
    if (keyword) params.keyword = keyword;
    setSearchParams(params);
  }

  return (
    <Spin spinning={loading}>
      <div style={{ minHeight: 320 }}>
        {!loading && products.length === 0 ? (
          <Empty
            style={{ marginTop: 80 }}
            description={keyword ? `没有找到与「${keyword}」相关的商品` : '暂无商品'}
          />
        ) : (
          <Row gutter={[16, 16]}>
            {products.map((p) => (
              // xs 2 列 / sm 3 列 / md 起 4 列（UI 决策 1 的响应式落地）
              <Col xs={12} sm={8} md={6} key={p.id}>
                <Card
                  hoverable
                  cover={<ProductCover product={p} />}
                  onClick={() => navigate(`/products/${p.id}`)}
                >
                  <Card.Meta
                    title={p.name}
                    description={
                      <div
                        style={{
                          display: 'flex',
                          justifyContent: 'space-between',
                          alignItems: 'center',
                        }}
                      >
                        <span style={{ color: '#cf1322', fontWeight: 500 }}>
                          {formatPrice(p.price_yuan)}
                        </span>
                        {/* 列表只标有货/售罄，不给具体数字（UI 决策 4） */}
                        {p.stock > 0 ? <Tag color="green">有货</Tag> : <Tag>售罄</Tag>}
                      </div>
                    }
                  />
                </Card>
              </Col>
            ))}
          </Row>
        )}
      </div>
      {total > PAGE_SIZE && (
        <div style={{ display: 'flex', justifyContent: 'center', marginTop: 24 }}>
          <Pagination
            current={page}
            total={total}
            pageSize={PAGE_SIZE}
            onChange={onPageChange}
            showSizeChanger={false}
          />
        </div>
      )}
    </Spin>
  );
}
