export function escapeHtml(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;');
}

export function formatCurrency(value) {
  const amount = Number(value);
  return Number.isFinite(amount)
    ? new Intl.NumberFormat('zh-CN', { style: 'currency', currency: 'CNY' }).format(amount)
    : '—';
}

export function formatDate(unixSeconds) {
  const timestamp = Number(unixSeconds);
  if (!Number.isFinite(timestamp)) return '—';

  return new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'short',
    hour12: false,
  }).format(new Date(timestamp * 1000));
}

export function errorMessage(error) {
  if (error?.isAuthError) return '登录已失效，请重新登录。';
  return error?.message || '操作未完成，请稍后重试。';
}

export function productRoute(id) {
  return `#/products/${encodeURIComponent(id)}`;
}
