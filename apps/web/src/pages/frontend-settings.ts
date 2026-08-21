import { themeManager, ThemeMode } from '../theme';
import { icon } from '../components/icons';
import { toast } from '../components/toast';

export function mountFrontendSettingsPage(container: HTMLElement): () => void {
  const themeOptions: { mode: ThemeMode; icon: string; title: string; desc: string }[] = [
    {
      mode: 'system',
      icon: 'brightness_auto',
      title: '跟随系统',
      desc: '自动匹配操作系统的浅色或深色外观偏好',
    },
    {
      mode: 'light',
      icon: 'light_mode',
      title: '明亮浅色',
      desc: '清新明快的经典白色背景与高对比度文本',
    },
    {
      mode: 'dark',
      icon: 'dark_mode',
      title: '深邃暗色',
      desc: '夜间舒适的深色背景，减少眩光与眼部疲劳',
    },
  ];

  function render() {
    const currentMode = themeManager.getMode();

    container.innerHTML = `
      <div class="page-container fade-in">
        <header class="flex items-center gap-3 mb-6">
          <a href="#/settings" class="btn btn-ghost btn-icon-sm" title="返回设置" style="text-decoration: none;">
            ${icon('arrow_back')}
          </a>
          <div>
            <h1 class="page-title">网页端外观设置</h1>
            <p class="page-description">自定义 FrostAgent 管理界面的主题模式与视觉风格</p>
          </div>
        </header>

        <section class="card p-6 flex flex-col gap-5 max-w-2xl">
          <div>
            <h2 class="text-base font-semibold">主题模式</h2>
            <p class="text-xs text-muted mt-1">选择最适合您当前工作环境的主题配色方案</p>
          </div>

          <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
            ${themeOptions
              .map((opt) => {
                const isActive = currentMode === opt.mode;
                return `
                <button
                  class="card p-4 text-left cursor-pointer hover-bg transition-all flex flex-col gap-2 relative ${
                    isActive ? 'border-primary' : ''
                  }"
                  data-mode="${opt.mode}"
                  style="${isActive ? 'border-color: var(--primary); box-shadow: 0 0 0 1px var(--primary);' : ''}"
                >
                  <div class="flex items-center justify-between">
                    <span class="text-primary">${icon(opt.icon)}</span>
                    ${isActive ? `<span class="badge badge-primary text-xs">当前</span>` : ''}
                  </div>
                  <div>
                    <h3 class="text-sm font-semibold">${escapeHtml(opt.title)}</h3>
                    <p class="text-xs text-muted mt-0.5 leading-relaxed">${escapeHtml(opt.desc)}</p>
                  </div>
                </button>
              `;
              })
              .join('')}
          </div>
        </section>
      </div>
    `;

    container.querySelectorAll<HTMLButtonElement>('button[data-mode]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const mode = btn.dataset.mode as ThemeMode;
        if (mode) {
          themeManager.setMode(mode);
          toast.success(`已切换为${mode === 'system' ? '跟随系统' : mode === 'light' ? '亮色' : '暗色'}模式`);
          render();
        }
      });
    });
  }

  function escapeHtml(str: string): string {
    return str
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#039;');
  }

  render();

  return () => {};
}
