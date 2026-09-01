import { escapeHtml } from '../utils.js';

export function showLoading(container, label = '正在加载数据…') {
  container.innerHTML = `<section class="state-card" aria-busy="true"><span class="spinner" aria-hidden="true"></span><p>${escapeHtml(label)}</p></section>`;
}

export function showError(container, message) {
  container.innerHTML = `<section class="state-card state-card--error" role="alert"><strong>未能加载内容</strong><p>${escapeHtml(message)}</p></section>`;
}

export function showEmpty(container, title, message, actionLabel) {
  container.innerHTML = `
    <section class="empty-state">
      <span class="empty-state__mark" aria-hidden="true">□</span>
      <h2>${escapeHtml(title)}</h2>
      <p>${escapeHtml(message)}</p>
      ${actionLabel ? '<a class="button button--primary" href="#/products/new">新建商品</a>' : ''}
    </section>`;
}

export function toast(message, type = 'success') {
  const region = document.querySelector('#toast-region');
  const item = document.createElement('div');
  item.className = `toast toast--${type}`;
  item.setAttribute('role', type === 'error' ? 'alert' : 'status');
  item.textContent = message;
  region.append(item);
  window.setTimeout(() => item.remove(), 4200);
}
