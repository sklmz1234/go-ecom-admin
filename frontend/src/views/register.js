import { register } from '../api.js';
import { errorMessage } from '../utils.js';

export function render(container) {
  container.innerHTML = `
    <main class="auth-page">
      <section class="auth-panel" aria-labelledby="register-title">
        <a class="auth-brand" href="#/products"><span class="brand-mark" aria-hidden="true">M</span>商户管理台</a>
        <div class="auth-heading">
          <p class="eyebrow">商品运营工作区</p>
          <h1 id="register-title">创建账户</h1>
          <p>填写以下信息后，即可登录管理商品。</p>
        </div>
        <form class="auth-form" data-register-form novalidate>
          <div class="field">
            <label for="username">用户名</label>
            <input id="username" name="username" autocomplete="username" required autofocus />
          </div>
          <div class="field">
            <label for="email">邮箱</label>
            <input id="email" name="email" type="email" autocomplete="email" required />
          </div>
          <div class="field">
            <label for="password">密码</label>
            <input id="password" name="password" type="password" autocomplete="new-password" minlength="6" required />
          </div>
          <p class="form-error" data-form-error role="alert" hidden></p>
          <button class="button button--primary button--block" type="submit">创建账户</button>
        </form>
        <p class="auth-footer">已有账户？<a href="#/login">返回登录</a></p>
      </section>
      <aside class="auth-aside" aria-hidden="true">
        <div class="auth-aside__content">
          <p>PRODUCT DESK</p>
          <strong>开始管理你的
商品目录。</strong>
        </div>
      </aside>
    </main>`;

  const form = container.querySelector('[data-register-form]');
  const error = form.querySelector('[data-form-error]');
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    error.hidden = true;
    const username = form.elements.username.value.trim();
    const email = form.elements.email.value.trim();
    const password = form.elements.password.value;

    if (!username || !form.elements.email.checkValidity() || password.length < 6) {
      error.textContent = '请填写用户名、有效邮箱和至少 6 位密码。';
      error.hidden = false;
      return;
    }

    const submit = form.querySelector('button[type="submit"]');
    submit.disabled = true;
    try {
      await register({ username, email, password });
      window.location.hash = '#/login?registered=1';
    } catch (requestError) {
      error.textContent = errorMessage(requestError);
      error.hidden = false;
    } finally {
      submit.disabled = false;
    }
  });
}
