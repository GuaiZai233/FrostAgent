import { themeManager, ThemeMode } from '../theme';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { escapeHtml } from '../utils/formatters';
import { getControlToken, setControlToken } from '../api/client';

export function mountFrontendSettingsPage(container: HTMLElement): () => void {
  const themeOptions: { mode: ThemeMode; icon: string; title: string; desc: string }[] = [
    {
      mode: 'system',
      icon: 'monitor',
      title: '跟随系统',
      desc: '自动匹配操作系统的浅色或深色外观偏好',
    },
    {
      mode: 'light',
      icon: 'sun',
      title: '明亮浅色',
      desc: '清新明快的经典浅色背景与高对比度文本',
    },
    {
      mode: 'dark',
      icon: 'moon',
      title: '深邃暗色',
      desc: '夜间舒适的深色背景，减少眩光与眼部疲劳',
    },
  ];

  function render() {
    const currentMode = themeManager.getMode();
    const currentToken = getControlToken();

    container.innerHTML = `
      <div class="page-container fade-in">
        <header class="flex items-center gap-2.5 pb-1">
          <a href="#/settings" class="btn btn-ghost btn-icon-sm" title="返回设置" style="text-decoration: none;">
            ${icon('arrow_left', 'w-4 h-4')}
          </a>
          <div>
            <h1 class="page-title">网页端设置</h1>
            <p class="page-description">自定义 FrostAgent 网页端视觉风格与服务端访问凭据</p>
          </div>
        </header>

        <section class="card p-4 flex flex-col gap-4" style="max-width: 42rem;">
          <div>
            <h2 class="text-sm font-semibold text-foreground">主题模式</h2>
            <p class="text-xs text-muted mt-0.5">选择最适合您当前工作环境的主题配色方案</p>
          </div>

          <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
            ${themeOptions
              .map((opt) => {
                const isActive = currentMode === opt.mode;
                return `
                <button
                  class="card p-3.5 text-left cursor-pointer hover-bg transition-all flex flex-col gap-2 relative ${
                    isActive ? 'border-primary ring-1 ring-primary' : ''
                  }"
                  data-mode="${opt.mode}"
                >
                  <div class="flex items-center justify-between">
                    <span class="${isActive ? 'text-primary' : 'text-muted'} flex items-center">${icon(
                  opt.icon,
                  'w-4 h-4',
                )}</span>
                    ${isActive ? `<span class="badge badge-primary text-[11px] px-1.5 py-0">当前</span>` : ''}
                  </div>
                  <div>
                    <h3 class="text-xs font-semibold text-foreground">${escapeHtml(opt.title)}</h3>
                    <p class="text-[11px] text-muted mt-0.5 leading-relaxed">${escapeHtml(opt.desc)}</p>
                  </div>
                </button>
              `;
              })
              .join('')}
          </div>
        </section>

        <section class="card p-4 flex flex-col gap-4 mt-1" style="max-width: 42rem;">
          <div>
            <h2 class="text-sm font-semibold text-foreground">API / 控制平面访问凭据</h2>
            <p class="text-xs text-muted mt-0.5">当服务端启用了 MCP_CONTROL_TOKEN 或 ADMIN_TOKEN，或通过非本地网络远程访问时，网页端将自动在请求头中携带此 Bearer Token 进行身份认证。</p>
          </div>

          <div class="flex flex-col gap-2.5">
            <div class="flex items-center gap-2">
              <input
                id="control-token-input"
                type="password"
                class="input text-xs flex-1"
                placeholder="输入 MCP_CONTROL_TOKEN 或 ADMIN_TOKEN (留空清除)"
                value="${escapeHtml(currentToken)}"
              />
              <button id="toggle-token-visibility" class="btn btn-secondary btn-sm" type="button" title="切换可见性">
                ${icon('eye', 'w-3.5 h-3.5')}
              </button>
            </div>
            <div class="flex items-center justify-between pt-1">
              <span class="text-xs text-muted">
                ${currentToken ? '<span class="text-emerald-500 font-medium">✓ 已配置 Token</span>' : '未设置 Token (本地同源访问将使用默认安全边界)'}
              </span>
              <div class="flex gap-2">
                ${currentToken ? `<button id="clear-token-btn" class="btn btn-ghost btn-sm text-destructive text-xs">清除</button>` : ''}
                <button id="save-token-btn" class="btn btn-primary btn-sm text-xs">保存凭据</button>
              </div>
            </div>
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

    const tokenInput = container.querySelector<HTMLInputElement>('#control-token-input');
    const toggleBtn = container.querySelector<HTMLButtonElement>('#toggle-token-visibility');
    const saveBtn = container.querySelector<HTMLButtonElement>('#save-token-btn');
    const clearBtn = container.querySelector<HTMLButtonElement>('#clear-token-btn');

    toggleBtn?.addEventListener('click', () => {
      if (tokenInput) {
        tokenInput.type = tokenInput.type === 'password' ? 'text' : 'password';
      }
    });

    saveBtn?.addEventListener('click', () => {
      if (tokenInput) {
        setControlToken(tokenInput.value);
        toast.success('访问凭据已保存');
        render();
      }
    });

    clearBtn?.addEventListener('click', () => {
      setControlToken('');
      toast.success('访问凭据已清除');
      render();
    });
  }

  render();

  return () => {};
}
