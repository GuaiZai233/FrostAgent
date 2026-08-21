import './styles/variables.css';
import './styles/base.css';
import './styles/components.css';
import './styles/layout.css';

import { themeManager } from './theme';
import { router } from './router';
import { icon } from './components/icons';

import { mountOverviewPage } from './pages/overview';
import { mountSessionsPage } from './pages/sessions';
import { mountMemoryPage } from './pages/memory';
import { mountDialoguePage } from './pages/dialogue';
import { mountLogsPage } from './pages/logs';
import { mountSettingsPage } from './pages/settings';
import { mountBackendSettingsPage } from './pages/backend-settings';
import { mountFrontendSettingsPage } from './pages/frontend-settings';

// Initialize Theme
themeManager.init();

const navItems = [
  { path: '/overview', label: '概览', iconName: 'dashboard' },
  { path: '/sessions', label: '会话管理', iconName: 'forum' },
  { path: '/memory', label: '记忆管理', iconName: 'psychology' },
  { path: '/dialogue', label: '人设对话', iconName: 'chat' },
  { path: '/logs', label: '日志查询', iconName: 'receipt_long' },
  { path: '/settings', label: '系统设置', iconName: 'settings' },
];

function buildNavigationLinks(isMobile = false): string {
  return navItems
    .map(
      (item) => `
      <a
        href="#${item.path}"
        class="nav-item ${isMobile ? 'mobile-nav-link' : ''}"
        data-path="${item.path}"
        style="text-decoration: none;"
      >
        <span class="nav-icon">${icon(item.iconName)}</span>
        <span class="nav-label">${item.label}</span>
      </a>
    `,
    )
    .join('');
}

function initAppShell(): void {
  const appEl = document.getElementById('app');
  if (!appEl) return;

  appEl.innerHTML = `
    <div class="app-layout">
      <!-- Desktop Sidebar -->
      <aside class="sidebar">
        <div class="sidebar-header">
          <div class="brand">
            <div class="brand-icon">
              ${icon('ac_unit')}
            </div>
            <div class="brand-info">
              <span class="brand-title">FrostAgent</span>
              <span class="brand-subtitle">管理控制台</span>
            </div>
          </div>
        </div>

        <nav class="sidebar-nav" aria-label="主要导航">
          ${buildNavigationLinks(false)}
        </nav>

        <div class="sidebar-footer">
          <button class="theme-toggle-btn" id="theme-toggle-desktop" title="切换主题">
            <span class="theme-icon">${icon('light_mode', 'sm')}</span>
            <span class="text-xs">主题模式</span>
          </button>
        </div>
      </aside>

      <!-- Mobile Top Bar -->
      <header class="mobile-top-bar">
        <button class="btn btn-ghost btn-icon-sm" id="mobile-menu-btn" aria-label="打开导航菜单">
          ${icon('menu')}
        </button>
        <div class="flex items-center gap-2">
          <span class="text-primary">${icon('ac_unit', 'sm')}</span>
          <span class="font-bold text-sm">FrostAgent</span>
        </div>
        <button class="btn btn-ghost btn-icon-sm" id="theme-toggle-mobile" aria-label="切换主题">
          ${icon('light_mode', 'sm')}
        </button>
      </header>

      <!-- Mobile Drawer Backdrop -->
      <div class="mobile-drawer-backdrop" id="drawer-backdrop"></div>

      <!-- Mobile Drawer -->
      <aside class="mobile-drawer" id="mobile-drawer">
        <div class="drawer-header flex items-center justify-between p-4 border-b">
          <div class="brand">
            <div class="brand-icon">
              ${icon('ac_unit')}
            </div>
            <div class="brand-info">
              <span class="brand-title">FrostAgent</span>
              <span class="brand-subtitle">控制台</span>
            </div>
          </div>
          <button class="btn btn-ghost btn-icon-sm" id="drawer-close-btn" aria-label="关闭菜单">
            ${icon('close', 'sm')}
          </button>
        </div>
        <nav class="sidebar-nav p-3" aria-label="移动端导航">
          ${buildNavigationLinks(true)}
        </nav>
      </aside>

      <!-- Main Content Container -->
      <main class="main-content" id="main-content">
        <div id="page-content"></div>
      </main>
    </div>
  `;

  const pageContainer = document.getElementById('page-content');
  const drawer = document.getElementById('mobile-drawer');
  const backdrop = document.getElementById('drawer-backdrop');
  const menuBtn = document.getElementById('mobile-menu-btn');
  const closeBtn = document.getElementById('drawer-close-btn');

  // Mobile drawer controls
  const openDrawer = () => {
    drawer?.classList.add('open');
    backdrop?.classList.add('open');
  };

  const closeDrawer = () => {
    drawer?.classList.remove('open');
    backdrop?.classList.remove('open');
  };

  menuBtn?.addEventListener('click', openDrawer);
  closeBtn?.addEventListener('click', closeDrawer);
  backdrop?.addEventListener('click', closeDrawer);

  document.querySelectorAll('.mobile-nav-link').forEach((link) => {
    link.addEventListener('click', closeDrawer);
  });

  // Quick theme toggle helper
  const handleQuickThemeToggle = () => {
    const current = themeManager.getMode();
    if (current === 'light') themeManager.setMode('dark');
    else if (current === 'dark') themeManager.setMode('system');
    else themeManager.setMode('light');
  };

  document.getElementById('theme-toggle-desktop')?.addEventListener('click', handleQuickThemeToggle);
  document.getElementById('theme-toggle-mobile')?.addEventListener('click', handleQuickThemeToggle);

  // Update active links on route navigation
  router.onNavigate((activePath) => {
    document.querySelectorAll('.nav-item').forEach((item) => {
      const linkPath = item.getAttribute('data-path');
      if (!linkPath) return;

      const isActive =
        activePath === linkPath ||
        (linkPath === '/settings' && activePath.startsWith('/settings'));

      if (isActive) {
        item.classList.add('active');
      } else {
        item.classList.remove('active');
      }
    });
  });

  // Register Routes
  router.register('/overview', '概览', mountOverviewPage);
  router.register('/sessions', '会话管理', mountSessionsPage);
  router.register('/memory', '记忆管理', mountMemoryPage);
  router.register('/dialogue', '人设对话', mountDialoguePage);
  router.register('/logs', '日志查询', mountLogsPage);
  router.register('/settings', '系统设置', mountSettingsPage);
  router.register('/settings/backend', 'Bot 服务端设置', mountBackendSettingsPage);
  router.register('/settings/frontend', '网页端外观设置', mountFrontendSettingsPage);

  // Initialize Router
  if (pageContainer) {
    router.init(pageContainer);
  }
}

// Start application
document.addEventListener('DOMContentLoaded', () => {
  initAppShell();
});
