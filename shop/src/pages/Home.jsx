import { Empty } from 'antd';
import { useSearchParams } from 'react-router-dom';

// 首页：搜索 + 商品卡片网格 + 分页（commit 8 实现）。
// keyword 从 URL query 读（/?keyword=xxx），顶栏搜索框负责写入。
export default function Home() {
  const [searchParams] = useSearchParams();
  const keyword = searchParams.get('keyword') || '';

  return (
    <Empty
      description={keyword ? `搜索「${keyword}」——商品列表见 commit 8` : '商品列表见 commit 8'}
    />
  );
}
