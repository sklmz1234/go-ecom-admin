import { escapeHtml } from '../utils.js';

export function productFormMarkup({ product = {}, submitLabel, cancelHref = '#/products' }) {
  return `
    <form class="editor-form" novalidate>
      <div class="form-grid">
        <div class="field field--full">
          <label for="product-name">商品名称</label>
          <input id="product-name" name="name" type="text" maxlength="255" required value="${escapeHtml(product.name)}" autocomplete="off" />
          <p class="field-hint">使用清晰、便于识别的商品名称。</p>
        </div>
        <div class="field">
          <label for="product-price">销售价格（元）</label>
          <input id="product-price" name="price_yuan" type="number" inputmode="decimal" min="0.01" step="0.01" required value="${escapeHtml(product.price_yuan)}" />
          <p class="field-hint">价格须大于 0，最多保留两位小数。</p>
        </div>
        <div class="field">
          <label for="product-stock">库存数量</label>
          <input id="product-stock" name="stock" type="number" inputmode="numeric" min="0" step="1" required value="${escapeHtml(product.stock)}" />
          <p class="field-hint">请输入 0 或更大的整数。</p>
        </div>
      </div>
      <p class="form-error" data-form-error role="alert" hidden></p>
      <div class="form-actions">
        <a class="button button--secondary" href="${escapeHtml(cancelHref)}">取消</a>
        <button class="button button--primary" type="submit">${escapeHtml(submitLabel)}</button>
      </div>
    </form>`;
}

export function readProductForm(form) {
  const name = form.elements.name.value.trim();
  const price = form.elements.price_yuan.value;
  const stock = form.elements.stock.value;

  if (!name) throw new Error('请输入商品名称。');
  if (!/^\d+(?:\.\d{1,2})?$/.test(price) || Number(price) <= 0) {
    throw new Error('销售价格须为大于 0 且最多两位小数的数字。');
  }
  if (!/^\d+$/.test(stock)) throw new Error('库存数量须为 0 或更大的整数。');

  return { name, price_yuan: Number(price), stock: Number(stock) };
}

export function setFormError(form, message = '') {
  const element = form.querySelector('[data-form-error]');
  element.textContent = message;
  element.hidden = !message;
}

export function setSubmitting(form, submitting) {
  form.querySelector('button[type="submit"]').disabled = submitting;
}
