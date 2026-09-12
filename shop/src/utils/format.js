// 后端时间戳是 Unix 秒（int64），乘 1000 转 JS 毫秒。
export function formatDate(ts) {
  return new Date(ts * 1000).toLocaleString('zh-CN');
}

// 价格网关已换算成元（price_yuan / total_yuan / unit_price_yuan），
// 前端只做两位小数展示，不做分→元换算。
export function formatPrice(yuan) {
  return `¥${Number(yuan).toFixed(2)}`;
}
