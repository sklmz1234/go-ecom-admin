import { Empty } from 'antd';
import { useParams } from 'react-router-dom';

// 订单详情（commit 11 实现）：唯一带 items（商品名快照/数量/单价快照）的接口。
export default function OrderDetail() {
  const { id } = useParams();
  return <Empty description={`订单 #${id} 详情见 commit 11`} />;
}
