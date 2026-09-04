import './styles/app.css';
import './styles/variables.css';
import './styles/base.css';
import './styles/components.css';
import './styles/layout.css';

import { themeManager, type ThemeMode } from './theme';
import { scaleManager } from './scale';
import { router } from './router';
import { icon } from './components/icons';

import { mountOverviewPage } from './pages/overview';
import { mountSessionsPage } from './pages/sessions';
import { mountMemoryPage } from './pages/memory';
import { mountDialoguePage } from './pages/dialogue';
import { mountPromptPage } from './pages/prompt';
import { mountStickersPage } from './pages/stickers';
import { mountLogsPage } from './pages/logs';
import { mountSettingsPage } from './pages/settings';
import { mountBackendSettingsPage } from './pages/backend-settings';
import { mountFrontendSettingsPage } from './pages/frontend-settings';
import { mountModelRouterPage } from './pages/model-router';

// Initialize Theme & UI Scale
themeManager.init();
scaleManager.init();

const navItems = [
  { path: '/overview', label: '概览', iconName: 'dashboard' },
  { path: '/model-router', label: '模型路由器', iconName: 'sliders' },
  { path: '/sessions', label: '会话管理', iconName: 'forum' },
  { path: '/memory', label: '记忆管理', iconName: 'psychology' },
  { path: '/dialogue', label: '人设对话', iconName: 'chat' },
  { path: '/prompt', label: 'Prompt 检查', iconName: 'sparkles' },
  { path: '/stickers', label: '表情包摘取', iconName: 'sticker' },
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
      >
        <span class="nav-icon">${icon(item.iconName)}</span>
        <span class="nav-label">${item.label}</span>
      </a>
    `,
    )
    .join('');
}

function getThemeIconAndLabel(mode: ThemeMode): { iconName: string; label: string } {
  switch (mode) {
    case 'light':
      return { iconName: 'sun', label: '浅色模式' };
    case 'dark':
      return { iconName: 'moon', label: '深色模式' };
    case 'system':
    default:
      return { iconName: 'sun_medium', label: '跟随系统' };
  }
}

function initAppShell(): void {
  const appEl = document.getElementById('app');
  if (!appEl) return;

  const currentTheme = themeManager.getMode();
  const themeInfo = getThemeIconAndLabel(currentTheme);

  appEl.innerHTML = `
    <div class="app-layout">
      <!-- Desktop Sidebar -->
      <aside class="sidebar">
        <div class="sidebar-header">
          <div class="brand">
            <div class="brand-icon">
              ${icon('sparkles')}
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
            <span class="flex items-center gap-2">
              <span id="theme-icon-desktop" class="inline-flex">${icon(themeInfo.iconName)}</span>
              <span id="theme-label-desktop" class="text-xs">${themeInfo.label}</span>
            </span>
            <span class="text-muted text-xs">切换</span>
          </button>
        </div>
      </aside>

      <!-- Mobile Top Bar -->
      <header class="mobile-top-bar">
        <button class="btn btn-ghost btn-icon-sm" id="mobile-menu-btn" aria-label="打开导航菜单">
          ${icon('menu')}
        </button>
        <div class="flex items-center gap-2">
          <div class="brand-icon" style="width: 1.5rem; height: 1.5rem; border-radius: var(--radius-sm);">
            ${icon('sparkles', '', 14)}
          </div>
          <span class="font-bold text-sm">FrostAgent</span>
        </div>
        <button class="btn btn-ghost btn-icon-sm" id="theme-toggle-mobile" aria-label="切换主题">
          <span id="theme-icon-mobile" class="inline-flex">${icon(themeInfo.iconName)}</span>
        </button>
      </header>

      <!-- Mobile Drawer Backdrop -->
      <div class="mobile-drawer-backdrop" id="drawer-backdrop"></div>

      <!-- Mobile Drawer -->
      <aside class="mobile-drawer" id="mobile-drawer">
        <div class="drawer-header flex items-center justify-between p-4 border-b">
          <div class="brand">
            <div class="brand-icon">
              ${icon('sparkles')}
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

  // Update theme UI elements
  const updateThemeUI = (mode: ThemeMode) => {
    const info = getThemeIconAndLabel(mode);
    const iconDesktop = document.getElementById('theme-icon-desktop');
    const labelDesktop = document.getElementById('theme-label-desktop');
    const iconMobile = document.getElementById('theme-icon-mobile');

    if (iconDesktop) iconDesktop.innerHTML = icon(info.iconName);
    if (labelDesktop) labelDesktop.textContent = info.label;
    if (iconMobile) iconMobile.innerHTML = icon(info.iconName);
  };

  // Quick theme toggle helper
  const handleQuickThemeToggle = () => {
    const current = themeManager.getMode();
    if (current === 'light') themeManager.setMode('dark');
    else if (current === 'dark') themeManager.setMode('system');
    else themeManager.setMode('light');
  };

  document.getElementById('theme-toggle-desktop')?.addEventListener('click', handleQuickThemeToggle);
  document.getElementById('theme-toggle-mobile')?.addEventListener('click', handleQuickThemeToggle);

  themeManager.onChange((mode) => {
    updateThemeUI(mode);
  });

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
  router.register('/prompt', 'Prompt 检查', mountPromptPage);
  router.register('/stickers', '表情包摘取', mountStickersPage);
  router.register('/logs', '日志查询', mountLogsPage);
  router.register('/settings', '系统设置', mountSettingsPage);
  router.register('/settings/backend', 'Bot 服务端设置', mountBackendSettingsPage);
  router.register('/settings/frontend', '网页端外观设置', mountFrontendSettingsPage);
  router.register('/model-router', '模型路由器', mountModelRouterPage);

  // Initialize Router
  if (pageContainer) {
    router.init(pageContainer);
  }
}

// Start application
document.addEventListener('DOMContentLoaded', () => {
  initAppShell();
});
