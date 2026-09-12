import { Empty } from 'antd';
import { useParams } from 'react-router-dom';

// 商品详情 + 下单弹窗（commit 9 实现，含 409 库存不足的可读提示）。
export default function ProductDetail() {
  const { id } = useParams();
  return <Empty description={`商品 #${id} 详情见 commit 9`} />;
}
