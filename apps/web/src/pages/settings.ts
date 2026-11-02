import { icon } from '../components/icons';
import { openQuickConfig } from '../components/instances';
import { instanceState } from '../instance-state';

export function mountSettingsPage(container: HTMLElement): () => void {
  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="pb-1">
        <h1 class="page-title">系统设置</h1>
      </header>

      <div class="flex flex-col gap-3" style="max-width: 42rem;">
        <a
          href="#/settings/storage"
          class="card p-4 hover-bg transition-colors text-foreground"
          style="display: flex; flex-direction: row; align-items: center; justify-content: space-between; gap: 1rem; text-decoration: none; width: 100%;"
        >
          <div style="display: flex; align-items: center; gap: 0.875rem; min-width: 0;">
            <div class="flex items-center justify-center w-9 h-9 rounded-md bg-muted text-primary" style="flex-shrink: 0;">
              ${icon('server', 'w-4 h-4')}
            </div>
            <div style="min-width: 0;">
              <h2 class="text-sm font-semibold text-foreground">数据库设置</h2>
              <p class="text-xs text-muted mt-0.5">选择 SQLite 或 PostgreSQL 并立即连接</p>
            </div>
          </div>
          <div style="display: flex; align-items: center; justify-content: center; flex-shrink: 0; margin-left: auto; color: var(--muted-foreground);">
            ${icon('chevron_right', 'w-4 h-4')}
          </div>
        </a>
        <button
          type="button"
          id="settings-quick-config"
          class="card p-4 hover-bg transition-colors text-foreground"
          style="display: flex; align-items: center; justify-content: space-between; gap: 1rem; text-align: left; width: 100%; cursor: pointer;"
          ${instanceState.selected ? '' : 'disabled'}
        >
          <div class="flex items-center gap-3.5">
            <div class="flex items-center justify-center w-9 h-9 rounded-md bg-muted text-primary" style="flex-shrink: 0;">
              ${icon('copy', 'w-4 h-4')}
            </div>
            <div>
              <h2 class="text-sm font-semibold text-foreground">快速配置</h2>
              <p class="text-xs text-muted mt-0.5">${instanceState.selected ? '从其他实例复用配置，密钥及凭据来源不会复制' : '请先创建或选择实例'}</p>
            </div>
          </div>
          ${icon('chevron_right', 'w-4 h-4')}
        </button>
        <a
          href="#/settings/backup"
          class="card p-4 hover-bg transition-colors text-foreground"
          style="display: flex; flex-direction: row; align-items: center; justify-content: space-between; gap: 1rem; text-decoration: none; width: 100%;"
        >
          <div style="display: flex; align-items: center; gap: 0.875rem; min-width: 0;">
            <div class="flex items-center justify-center w-9 h-9 rounded-md bg-muted text-primary" style="flex-shrink: 0;">
              ${icon('download', 'w-4 h-4')}
            </div>
            <div style="min-width: 0;">
              <h2 class="text-sm font-semibold text-foreground">备份与还原</h2>
              <p class="text-xs text-muted mt-0.5">一键全量备份与各部分快速备份</p>
            </div>
          </div>
          <div style="display: flex; align-items: center; justify-content: center; flex-shrink: 0; margin-left: auto; color: var(--muted-foreground);">
            ${icon('chevron_right', 'w-4 h-4')}
          </div>
        </a>
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
              <p class="text-xs text-muted mt-0.5">修改数据库中的服务端设置、主动回复与群聊响应策略</p>
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
              <p class="text-xs text-muted mt-0.5">切换浅色、深色主题模式或跟随系统首选项</p>
            </div>
          </div>
          <div style="display: flex; align-items: center; justify-content: center; flex-shrink: 0; margin-left: auto; color: var(--muted-foreground);">
            ${icon('chevron_right', 'w-4 h-4')}
          </div>
        </a>

        <a
          href="#/settings/commands"
          class="card p-4 hover-bg transition-colors text-foreground"
          style="display: flex; flex-direction: row; align-items: center; justify-content: space-between; gap: 1rem; text-decoration: none; width: 100%;"
        >
          <div style="display: flex; align-items: center; gap: 0.875rem; min-width: 0;">
            <div class="flex items-center justify-center w-9 h-9 rounded-md bg-muted text-primary" style="flex-shrink: 0;">
              ${icon('terminal', 'w-4 h-4')}
            </div>
            <div style="min-width: 0;">
              <h2 class="text-sm font-semibold text-foreground">指令设置</h2>
              <p class="text-xs text-muted mt-0.5">配置管理员消息指令前缀与查看指令示例</p>
            </div>
          </div>
          <div style="display: flex; align-items: center; justify-content: center; flex-shrink: 0; margin-left: auto; color: var(--muted-foreground);">
            ${icon('chevron_right', 'w-4 h-4')}
          </div>
        </a>
      </div>
    </div>
  `;

  container.querySelector('#settings-quick-config')?.addEventListener('click', () => {
    void openQuickConfig();
  });

  return () => {};
}
