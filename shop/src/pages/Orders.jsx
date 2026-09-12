import { useCallback, useEffect, useState } from 'react';
import { App, Button, Popconfirm, Table, Tag } from 'antd';
import { Link } from 'react-router-dom';
import { cancelOrder, listMyOrders } from '../api';
import { formatDate, formatPrice } from '../utils/format';

const PAGE_SIZE = 10;

// 状态标签配色（计划定死）：PENDING 橙、PAID 绿、CANCELLED 灰。
const STATUS_TAG = {
  PENDING: { color: 'orange', text: '待支付' },
  PAID: { color: 'green', text: '已支付' },
  CANCELLED: { color: 'default', text: '已取消' },
};

function StatusTag({ status }) {
  const conf = STATUS_TAG[status] || { color: 'default', text: status };
  return <Tag color={conf.color}>{conf.text}</Tag>;
}

export default function Orders() {
  const { message } = App.useApp();
  const [orders, setOrders] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [cancellingId, setCancellingId] = useState(null);

  const fetchOrders = useCallback((targetPage) => {
    setLoading(true);
    listMyOrders({ page: targetPage, pageSize: PAGE_SIZE })
      .then((data) => {
        setOrders(data.orders || []);
        setTotal(data.total || 0);
      })
      .catch((e) => message.error(e.message))
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    fetchOrders(page);
  }, [page, fetchOrders]);

  async function onCancel(orderId) {
    setCancellingId(orderId);
    try {
      await cancelOrder(orderId);
      // 取消走 4A outbox 异步回补库存，message 里把这个语义说出来。
      message.success('订单已取消，库存将自动回补');
      // 就地刷新当前页，不跳页（计划要求）。
      fetchOrders(page);
    } catch (e) {
      message.error(e.message);
    } finally {
      setCancellingId(null);
    }
  }

  const columns = [
    { title: '订单号', dataIndex: 'id', render: (id) => `#${id}` },
    {
      title: '状态',
      dataIndex: 'status',
      render: (status) => <StatusTag status={status} />,
    },
    {
      title: '金额',
      dataIndex: 'total_yuan',
      render: (v) => formatPrice(v),
    },
    {
      title: '下单时间',
      dataIndex: 'created_at',
      render: (ts) => formatDate(ts),
      responsive: ['md'],
    },
    {
      title: '操作',
      key: 'action',
      render: (_, order) => (
        <span style={{ display: 'flex', gap: 8 }}>
          <Link to={`/orders/${order.id}`}>详情</Link>
          {/* 列表接口不带 items，明细必须进详情页（契约如此）。
              只有 PENDING 能取消——后端 CancelOrder 是条件更新，其他状态点了也是 409。 */}
          {order.status === 'PENDING' && (
            <Popconfirm
              title="确定取消这个订单吗？"
              okText="取消订单"
              cancelText="再想想"
              onConfirm={() => onCancel(order.id)}
            >
              <Button type="link" danger size="small" loading={cancellingId === order.id}>
                取消
              </Button>
            </Popconfirm>
          )}
        </span>
      ),
    },
  ];

  return (
    <Table
      rowKey="id"
      columns={columns}
      dataSource={orders}
      loading={loading}
      locale={{ emptyText: '还没有订单，去首页逛逛吧' }}
      pagination={{
        current: page,
        total,
        pageSize: PAGE_SIZE,
        showSizeChanger: false,
        onChange: setPage,
      }}
    />
  );
}
