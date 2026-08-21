import { icon } from '../components/icons';

export function mountSettingsPage(container: HTMLElement): () => void {
  container.innerHTML = `
    <div class="page-container fade-in" style="max-width: 680px;">
      <header class="pb-1">
        <h1 class="page-title">系统设置</h1>
        <p class="page-description">配置 FrostAgent 后端参数与网页外观主题</p>
      </header>

      <div class="flex flex-col gap-3">
        <a
          href="#/settings/backend"
          class="card p-4 hover-bg transition-colors flex items-center justify-between gap-3 text-foreground"
          style="text-decoration: none;"
        >
          <div class="flex items-center gap-3">
            <div class="flex items-center justify-center w-9 h-9 rounded-md bg-muted text-primary flex-shrink-0">
              ${icon('server', 'w-4 h-4')}
            </div>
            <div>
              <h2 class="text-sm font-semibold text-foreground">Bot 服务端设置</h2>
              <p class="text-xs text-muted mt-0.5">修改服务端环境变量、群聊响应策略与原始 .env 配置</p>
            </div>
          </div>
          <span class="text-muted flex items-center flex-shrink-0">${icon('chevron_right', 'w-4 h-4')}</span>
        </a>

        <a
          href="#/settings/frontend"
          class="card p-4 hover-bg transition-colors flex items-center justify-between gap-3 text-foreground"
          style="text-decoration: none;"
        >
          <div class="flex items-center gap-3">
            <div class="flex items-center justify-center w-9 h-9 rounded-md bg-muted text-primary flex-shrink-0">
              ${icon('palette', 'w-4 h-4')}
            </div>
            <div>
              <h2 class="text-sm font-semibold text-foreground">网页端外观设置</h2>
              <p class="text-xs text-muted mt-0.5">切换浅色、深色主题模式或跟随系统首选项</p>
            </div>
          </div>
          <span class="text-muted flex items-center flex-shrink-0">${icon('chevron_right', 'w-4 h-4')}</span>
        </a>
      </div>
    </div>
  `;

  return () => {};
}
