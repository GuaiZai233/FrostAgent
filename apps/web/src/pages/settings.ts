import { icon } from '../components/icons';

export function mountSettingsPage(container: HTMLElement): () => void {
  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="pb-1">
        <h1 class="page-title">系统设置</h1>
        <p class="page-description">配置 FrostAgent 后端参数与网页外观主题</p>
      </header>

      <div class="flex flex-col gap-3" style="max-width: 42rem;">
        <a
          href="#/settings/backend"
          class="card p-4 hover-bg transition-colors text-foreground"
          style="display: flex; flex-direction: row; align-items: center; justify-content: space-between; gap: 1rem; text-decoration: none; width: 100%;"
        >
          <div style="display: flex; align-items: center; gap: 0.875rem; min-width: 0;">
            <div class="flex items-center justify-center w-9 h-9 rounded-md bg-muted text-primary" style="flex-shrink: 0;">
              ${icon('server', 'w-4 h-4')}
            </div>
            <div style="min-width: 0;">
              <h2 class="text-sm font-semibold text-foreground">Bot 服务端设置</h2>
              <p class="text-xs text-muted mt-0.5">修改服务端环境变量、群聊响应策略与原始 .env 配置</p>
            </div>
          </div>
          <div style="display: flex; align-items: center; justify-content: center; flex-shrink: 0; margin-left: auto; color: var(--muted-foreground);">
            ${icon('chevron_right', 'w-4 h-4')}
          </div>
        </a>

        <a
          href="#/settings/frontend"
          class="card p-4 hover-bg transition-colors text-foreground"
          style="display: flex; flex-direction: row; align-items: center; justify-content: space-between; gap: 1rem; text-decoration: none; width: 100%;"
        >
          <div style="display: flex; align-items: center; gap: 0.875rem; min-width: 0;">
            <div class="flex items-center justify-center w-9 h-9 rounded-md bg-muted text-primary" style="flex-shrink: 0;">
              ${icon('palette', 'w-4 h-4')}
            </div>
            <div style="min-width: 0;">
              <h2 class="text-sm font-semibold text-foreground">网页端外观设置</h2>
              <p class="text-xs text-muted mt-0.5">切换浅色与深色主题模式，调整界面整体缩放比例</p>
            </div>
          </div>
          <div style="display: flex; align-items: center; justify-content: center; flex-shrink: 0; margin-left: auto; color: var(--muted-foreground);">
            ${icon('chevron_right', 'w-4 h-4')}
          </div>
        </a>
      </div>
    </div>
  `;

  return () => {};
}
