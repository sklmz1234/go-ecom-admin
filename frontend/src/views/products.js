import { listProducts } from '../api.js';
import { isAuthenticated } from '../store.js';
import { escapeHtml, formatCurrency, formatDate, errorMessage, productRoute } from '../utils.js';
import { showEmpty, showError, showLoading } from '../components/feedback.js';

const PAGE_SIZE = 10;

export async function render(container, { page = 1 } = {}) {
  showLoading(container, '正在读取商品目录…');
  try {
    const data = await listProducts({ page, pageSize: PAGE_SIZE });
    const products = data.products || [];
    const total = Number(data.total) || 0;
    const currentPage = Math.max(1, page);

    if (!products.length) {
      // 仅在目录本身为空时展示引导，避免越界页被误判为空目录。
      if (total === 0) {
        showEmpty(container, '还没有商品', isAuthenticated() ? '从第一件商品开始建立你的商品目录。' : '登录后即可开始创建商品。', isAuthenticated());
      } else {
        showEmpty(container, '当前页没有商品', '此页没有可显示的商品，请返回上一页继续查看。');
      }
      return;
    }

    const start = (currentPage - 1) * PAGE_SIZE + 1;
    const end = Math.min(start + products.length - 1, total);
    container.innerHTML = `
      <section class="page-header">
        <div>
          <p class="eyebrow">PRODUCT CATALOG</p>
          <h1>商品管理</h1>
          <p>查看商品信息与当前库存。</p>
        </div>
        ${isAuthenticated() ? '<a class="button button--primary" href="#/products/new">新建商品</a>' : '<a class="button button--secondary" href="#/login">登录后管理商品</a>'}
      </section>
      <section class="content-card">
        <div class="card-header">
          <div><h2>全部商品</h2><p>共 ${total} 件商品</p></div>
          <p class="data-note">价格单位：人民币元</p>
        </div>
        <div class="table-wrap">
          <table>
            <thead><tr><th scope="col">商品名称</th><th scope="col">销售价格</th><th scope="col">库存</th><th scope="col">创建时间</th><th scope="col"><span class="sr-only">操作</span></th></tr></thead>
            <tbody>
              ${products.map((product) => `
                <tr>
                  <th scope="row"><a class="table-link" href="${productRoute(product.id)}">${escapeHtml(product.name)}</a></th>
                  <td>${formatCurrency(product.price_yuan)}</td>
                  <td><span class="stock-value ${Number(product.stock) === 0 ? 'stock-value--empty' : ''}">${escapeHtml(product.stock)}</span></td>
                  <td class="muted">${formatDate(product.created_at)}</td>
                  <td class="table-action"><a class="text-link" href="${productRoute(product.id)}">查看</a></td>
                </tr>`).join('')}
            </tbody>
          </table>
        </div>
        <footer class="pagination" aria-label="商品分页">
          <p>显示第 ${start}–${end} 件，共 ${total} 件</p>
          <div>
            <a class="button button--secondary button--small ${currentPage <= 1 ? 'is-disabled' : ''}" ${currentPage <= 1 ? 'aria-disabled="true" tabindex="-1"' : `href="#/products?page=${currentPage - 1}"`}>上一页</a>
            <span class="page-indicator">第 ${currentPage} 页</span>
            <a class="button button--secondary button--small ${end >= total ? 'is-disabled' : ''}" ${end >= total ? 'aria-disabled="true" tabindex="-1"' : `href="#/products?page=${currentPage + 1}"`}>下一页</a>
          </div>
        </footer>
      </section>`;
  } catch (requestError) {
    showError(container, errorMessage(requestError));
  }
}
