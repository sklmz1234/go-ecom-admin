import { deleteProduct, getProduct } from '../api.js';
import { isAuthenticated } from '../store.js';
import { confirmDialog } from '../components/dialog.js';
import { showError, showLoading, toast } from '../components/feedback.js';
import { escapeHtml, errorMessage, formatCurrency, formatDate } from '../utils.js';

export async function render(container, { id }) {
  showLoading(container, '正在读取商品详情…');
  try {
    const product = await getProduct(id);
    container.innerHTML = `
      <section class="page-header page-header--detail">
        <div>
          <a class="back-link" href="#/products">← 返回商品管理</a>
          <p class="eyebrow">PRODUCT DETAIL</p>
          <h1>${escapeHtml(product.name)}</h1>
          <p>查看商品价格、库存及创建记录。</p>
        </div>
        ${isAuthenticated() ? `
          <div class="action-group">
            <a class="button button--secondary" href="#/products/${encodeURIComponent(id)}/edit">编辑商品</a>
            <button class="button button--danger" type="button" data-delete-product>删除商品</button>
          </div>` : '<a class="button button--secondary" href="#/login">登录后管理商品</a>'}
      </section>
      <section class="detail-grid" aria-label="商品信息">
        <article class="content-card detail-card detail-card--price">
          <p class="detail-label">销售价格</p>
          <strong>${formatCurrency(product.price_yuan)}</strong>
          <span>人民币元</span>
        </article>
        <article class="content-card detail-card">
          <p class="detail-label">当前库存</p>
          <strong>${escapeHtml(product.stock)}</strong>
          <span>件</span>
        </article>
        <article class="content-card detail-card">
          <p class="detail-label">创建时间</p>
          <strong class="detail-date">${formatDate(product.created_at)}</strong>
          <span>系统记录时间</span>
        </article>
      </section>
      <section class="content-card property-card">
        <h2>基础信息</h2>
        <dl class="property-list">
          <div><dt>商品编号</dt><dd>${escapeHtml(product.id)}</dd></div>
          <div><dt>商品名称</dt><dd>${escapeHtml(product.name)}</dd></div>
          <div><dt>销售价格</dt><dd>${formatCurrency(product.price_yuan)}</dd></div>
          <div><dt>库存数量</dt><dd>${escapeHtml(product.stock)} 件</dd></div>
        </dl>
      </section>`;

    container.querySelector('[data-delete-product]')?.addEventListener('click', async (event) => {
      const button = event.currentTarget;
      const confirmed = await confirmDialog({
        title: '删除此商品？',
        description: `“${product.name}”将被永久删除，且无法恢复。`,
      });
      if (!confirmed) return;

      button.disabled = true;
      try {
        await deleteProduct(id);
        toast('商品已删除。');
        window.location.hash = '#/products';
      } catch (requestError) {
        button.disabled = false;
        // 认证失效须交由路由统一清理并跳转登录页。
        if (requestError?.isAuthError) throw requestError;
        toast(errorMessage(requestError), 'error');
      }
    });
  } catch (requestError) {
    showError(container, errorMessage(requestError));
  }
}
