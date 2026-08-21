import { icon } from '../components/icons';

export function mountSettingsPage(container: HTMLElement): () => void {
  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="mb-6">
        <h1 class="page-title">系统设置</h1>
        <p class="page-description">配置 FrostAgent 后端参数与网页外观主题</p>
      </header>

      <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <a
          href="#/settings/backend"
          class="card p-5 cursor-pointer hover-bg transition-colors flex items-center gap-4 text-foreground"
          style="text-decoration: none;"
        >
          <div class="flex items-center justify-center w-12 h-12 rounded-xl bg-muted text-primary">
            ${icon('dns')}
          </div>
          <div>
            <h2 class="card-title text-base font-semibold">Bot 服务端设置</h2>
            <p class="card-description text-xs text-muted mt-1">快捷修改服务端环境变量、群聊行为与原始 .env 文件</p>
          </div>
        </a>

        <a
          href="#/settings/frontend"
          class="card p-5 cursor-pointer hover-bg transition-colors flex items-center gap-4 text-foreground"
          style="text-decoration: none;"
        >
          <div class="flex items-center justify-center w-12 h-12 rounded-xl bg-muted text-primary">
            ${icon('palette')}
          </div>
          <div>
            <h2 class="card-title text-base font-semibold">网页端外观设置</h2>
            <p class="card-description text-xs text-muted mt-1">切换亮色/暗色主题模式或跟随系统首选项</p>
          </div>
        </a>
      </div>
    </div>
  `;

  return () => {};
}
