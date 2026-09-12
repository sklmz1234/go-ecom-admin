import { useEffect, useState } from 'react';
import { App, Button, Card, Descriptions, Spin, Table, Tag } from 'antd';
import { useNavigate, useParams } from 'react-router-dom';
import { getOrder } from '../api';
import { formatDate, formatPrice } from '../utils/format';

const STATUS_TAG = {
  PENDING: { color: 'orange', text: '待支付' },
  PAID: { color: 'green', text: '已支付' },
  CANCELLED: { color: 'default', text: '已取消' },
};

// 订单详情是唯一带 items 的接口（列表靠 omitempty 共用 DTO 不带），
// items 里的商品名和单价是下单时刻的快照——商品以后改名改价不影响这里。
export default function OrderDetail() {
  const { id } = useParams();
  const navigate = useNavigate();
  const { message } = App.useApp();
  const [order, setOrder] = useState(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    getOrder(id)
      .then((data) => !cancelled && setOrder(data))
      .catch((e) => !cancelled && message.error(e.message))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  if (loading) {
    return (
      <div style={{ textAlign: 'center', padding: 80 }}>
        <Spin size="large" />
      </div>
    );
  }
  if (!order) return null;

  const status = STATUS_TAG[order.status] || { color: 'default', text: order.status };

  const itemColumns = [
    { title: '商品', dataIndex: 'product_name' },
    { title: '数量', dataIndex: 'quantity', render: (q) => `×${q}` },
    {
      title: '单价（下单快照）',
      dataIndex: 'unit_price_yuan',
      render: (v) => formatPrice(v),
    },
    {
      title: '小计',
      key: 'subtotal',
      render: (_, item) => formatPrice(item.unit_price_yuan * item.quantity),
    },
  ];

  return (
    <Card
      title={`订单 #${order.id}`}
      extra={<Button onClick={() => navigate('/orders')}>返回列表</Button>}
    >
      <Descriptions column={{ xs: 1, sm: 2 }} style={{ marginBottom: 24 }}>
        <Descriptions.Item label="状态">
          <Tag color={status.color}>{status.text}</Tag>
        </Descriptions.Item>
        <Descriptions.Item label="下单时间">{formatDate(order.created_at)}</Descriptions.Item>
        <Descriptions.Item label="订单总额">
          <span style={{ color: '#cf1322', fontWeight: 500 }}>{formatPrice(order.total_yuan)}</span>
        </Descriptions.Item>
      </Descriptions>
      <Table
        rowKey="product_id"
        columns={itemColumns}
        dataSource={order.items || []}
        pagination={false}
      />
    </Card>
  );
}
