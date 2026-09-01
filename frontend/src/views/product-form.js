import { createProduct, getProduct, updateProduct } from '../api.js';
import { productFormMarkup, readProductForm, setFormError, setSubmitting } from '../components/product-form.js';
import { showError, showLoading, toast } from '../components/feedback.js';
import { errorMessage, escapeHtml } from '../utils.js';

export async function render(container, { mode, id }) {
  const editing = mode === 'edit';
  let product = {};

  if (editing) {
    showLoading(container, '正在读取商品信息…');
    try {
      product = await getProduct(id);
    } catch (requestError) {
      showError(container, errorMessage(requestError));
      return;
    }
  }

  container.innerHTML = `
    <section class="page-header">
      <div>
        <a class="back-link" href="${editing ? `#/products/${encodeURIComponent(id)}` : '#/products'}">← ${editing ? '返回商品详情' : '返回商品管理'}</a>
        <p class="eyebrow">${editing ? 'EDIT PRODUCT' : 'NEW PRODUCT'}</p>
        <h1>${editing ? '编辑商品' : '新建商品'}</h1>
        <p>${editing ? `正在修改“${escapeHtml(product.name)}”的完整商品信息。` : '填写基础信息，创建后可在商品列表中查看。'}</p>
      </div>
    </section>
    <section class="content-card editor-card">
      <div class="card-header"><div><h2>商品信息</h2><p>带 * 的字段为必填项。</p></div></div>
      ${productFormMarkup({
        product,
        submitLabel: editing ? '保存修改' : '创建商品',
        cancelHref: editing ? `#/products/${encodeURIComponent(id)}` : '#/products',
      })}
    </section>`;

  const form = container.querySelector('.editor-form');
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    setFormError(form);
    let values;
    try {
      values = readProductForm(form);
    } catch (validationError) {
      setFormError(form, validationError.message);
      return;
    }

    setSubmitting(form, true);
    try {
      const savedProduct = editing ? await updateProduct(id, values) : await createProduct(values);
      toast(editing ? '商品信息已保存。' : '商品已创建。');
      // 创建接口返回实体时直达详情，缺少编号时回到列表仍能看到新记录。
      window.location.hash = savedProduct?.id ? `#/products/${encodeURIComponent(savedProduct.id)}` : '#/products';
    } catch (requestError) {
      // 认证失效须交由路由统一清理并跳转登录页。
      if (requestError?.isAuthError) throw requestError;
      setFormError(form, errorMessage(requestError));
    } finally {
      setSubmitting(form, false);
    }
  });
}
