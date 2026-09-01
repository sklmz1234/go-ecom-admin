import { getSession, clearSession } from '../store.js';
import { escapeHtml } from '../utils.js';

export function renderAppLayout(container) {
  const session = getSession();
  const username = session?.user?.username || session?.user?.name || '已登录用户';
  const accountMarkup = session ? `
    <div class="user-menu">
      <span class="user-avatar" aria-hidden="true">${escapeHtml(username.slice(0, 1).toUpperCase())}</span>
      <span class="user-name">${escapeHtml(username)}</span>
      <button class="text-button" type="button" data-logout>退出登录</button>
    </div>` : '<a class="button button--secondary button--small" href="#/login">登录</a>';

  container.innerHTML = `
    <div class="app-shell">
      <div class="sidebar-backdrop" data-sidebar-backdrop hidden></div>
      <aside class="sidebar" aria-label="主导航">
        <a class="brand" href="#/products" aria-label="商户管理台首页">
          <span class="brand-mark" aria-hidden="true">M</span>
          <span>商户管理台</span>
        </a>
        <nav class="side-nav" aria-label="功能导航">
          <a class="side-nav__link" href="#/products" data-nav-products>
            <span aria-hidden="true">□</span><span>商品管理</span>
          </a>
        </nav>
        <p class="sidebar-note">库存与商品信息以服务端数据为准</p>
      </aside>
      <section class="app-frame">
        <header class="topbar">
          <button class="icon-button mobile-menu" type="button" aria-label="打开导航" aria-expanded="false" data-menu-toggle>
            <span></span><span></span><span></span>
          </button>
          <div class="topbar__spacer"></div>
          ${accountMarkup}
        </header>
        <main class="page-content" id="page-content" tabindex="-1"></main>
      </section>
    </div>`;

  const sidebar = container.querySelector('.sidebar');
  const backdrop = container.querySelector('[data-sidebar-backdrop]');
  const menuToggle = container.querySelector('[data-menu-toggle]');

  function closeMenu() {
    sidebar.classList.remove('sidebar--open');
    backdrop.hidden = true;
    menuToggle.setAttribute('aria-expanded', 'false');
  }

  menuToggle.addEventListener('click', () => {
    const open = !sidebar.classList.contains('sidebar--open');
    sidebar.classList.toggle('sidebar--open', open);
    backdrop.hidden = !open;
    menuToggle.setAttribute('aria-expanded', String(open));
  });
  backdrop.addEventListener('click', closeMenu);
  container.querySelector('[data-nav-products]').addEventListener('click', closeMenu);
  // 游客没有退出控件，按需绑定避免公开页面渲染时报错。
  container.querySelector('[data-logout]')?.addEventListener('click', () => {
    clearSession();
    window.location.hash = '#/login';
  });

  return container.querySelector('#page-content');
}

export function setActiveNavigation(container, routeName) {
  container.querySelector('[data-nav-products]')?.classList.toggle('side-nav__link--active', routeName === 'products');
}
