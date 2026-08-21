import { api } from '../api/client';
import { BotStatus } from '@frostagent/proto';
import { formatCount, formatStatus, formatUptime, escapeHtml } from '../utils/formatters';
import { icon } from '../components/icons';

export function mountOverviewPage(container: HTMLElement): () => void {
  let timerId: number | null = null;
  let isUnmounted = false;

  container.innerHTML = `
    <div class="page-container fade-in">
      <div id="overview-loading" class="flex items-center gap-2 text-xs text-muted">
        <span class="spinner"></span>
        <span>加载概览数据...</span>
      </div>
      <div id="overview-error" style="display: none;"></div>
      <div id="overview-content" style="display: none;" class="flex flex-col gap-5"></div>
    </div>
  `;

  const loadingEl = container.querySelector<HTMLElement>('#overview-loading')!;
  const errorEl = container.querySelector<HTMLElement>('#overview-error')!;
  const contentEl = container.querySelector<HTMLElement>('#overview-content')!;

  async function loadData(showLoading = false) {
    if (isUnmounted) return;
    if (showLoading && contentEl.children.length === 0) {
      loadingEl.style.display = 'flex';
    }

    try {
      const data = await api.getOverview();
      if (isUnmounted) return;

      loadingEl.style.display = 'none';
      errorEl.style.display = 'none';
      contentEl.style.display = 'flex';

      const statusBadgeClass =
        data.status === BotStatus.RUNNING
          ? 'badge-success'
          : data.status === BotStatus.INITIALIZING
          ? 'badge-warning'
          : data.status === BotStatus.ERROR
          ? 'badge-destructive'
          : 'badge-outline';

      const botName = data.botName || 'FrostAgent';
      const version = data.version || '-';

      const toolsHtml =
        data.tools.length > 0
          ? data.tools
              .map(
                (tool) => `
            <article class="card p-3.5 flex flex-col gap-1.5 hover-bg transition-colors">
              <div class="flex items-center gap-2">
                <span class="text-primary flex items-center">${icon('wrench', 'w-4 h-4')}</span>
                <h3 class="text-xs font-semibold text-foreground">${escapeHtml(tool.name)}</h3>
              </div>
              <p class="text-xs text-muted leading-relaxed">
                ${escapeHtml(tool.description || '无描述')}
              </p>
            </article>
          `,
              )
              .join('')
          : `
          <div class="card p-6 text-center text-muted col-span-3 text-xs">
            暂无已注册工具。
          </div>
        `;

      contentEl.innerHTML = `
        <header class="flex flex-col gap-2 pb-1">
          <div class="flex items-center justify-between gap-3 flex-wrap">
            <h1 class="page-title text-xl font-bold">
              你好👋！我是 ${escapeHtml(botName)}
            </h1>
            <div class="flex flex-wrap gap-1.5 items-center">
              <span class="badge ${statusBadgeClass}">
                ${icon('activity', 'w-3 h-3')}
                ${escapeHtml(formatStatus(data.status))}
              </span>
              <span class="badge badge-outline">
                ${icon('clock', 'w-3 h-3')}
                ${escapeHtml(formatUptime(data.uptimeSeconds))}
              </span>
              <span class="badge badge-outline">
                ${icon('code', 'w-3 h-3')}
                v${escapeHtml(version)}
              </span>
            </div>
          </div>
          <p class="page-description">智能体核心服务运行状态与已挂载工具能力</p>
        </header>

        <!-- KPI Summary Cards -->
        <section class="grid grid-cols-1 sm:grid-cols-3 gap-3" aria-label="Bot statistics">
          <article class="card p-3.5 flex items-center gap-3">
            <div class="flex items-center justify-center w-9 h-9 rounded-md bg-muted text-primary flex-shrink-0">
              ${icon('message_square', 'w-4 h-4')}
            </div>
            <div class="min-w-0">
              <p class="text-xs text-muted font-medium">处理消息</p>
              <p class="text-lg font-bold tracking-tight text-foreground">${escapeHtml(formatCount(data.totalMessagesProcessed))}</p>
            </div>
          </article>

          <article class="card p-3.5 flex items-center gap-3">
            <div class="flex items-center justify-center w-9 h-9 rounded-md bg-muted text-primary flex-shrink-0">
              ${icon('users', 'w-4 h-4')}
            </div>
            <div class="min-w-0">
              <p class="text-xs text-muted font-medium">活跃会话</p>
              <p class="text-lg font-bold tracking-tight text-foreground">${escapeHtml(formatCount(data.activeSessions))}</p>
            </div>
          </article>

          <article class="card p-3.5 flex items-center gap-3">
            <div class="flex items-center justify-center w-9 h-9 rounded-md bg-muted text-primary flex-shrink-0">
              ${icon('brain', 'w-4 h-4')}
            </div>
            <div class="min-w-0">
              <p class="text-xs text-muted font-medium">当前模型</p>
              <p class="text-sm font-bold truncate text-foreground" title="${escapeHtml(data.currentModel || '-')}">
                ${escapeHtml(data.currentModel || '-')}
              </p>
            </div>
          </article>
        </section>

        <!-- Tools Capability Section -->
        <section class="flex flex-col gap-3">
          <div class="flex items-center justify-between">
            <h2 class="text-sm font-semibold text-foreground flex items-center gap-1.5">
              <span>已注册工具能力</span>
              <span class="badge badge-outline text-xs">${data.tools.length}</span>
            </h2>
          </div>
          <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
            ${toolsHtml}
          </div>
        </section>
      `;
    } catch (err) {
      if (isUnmounted) return;
      loadingEl.style.display = 'none';
      errorEl.style.display = 'block';
      errorEl.innerHTML = `
        <div class="card p-3.5 border-destructive text-destructive flex items-center gap-2 text-xs">
          ${icon('circle_alert', 'w-4 h-4')}
          <span>${escapeHtml(err instanceof Error ? err.message : String(err))}</span>
        </div>
      `;
    }
  }

  void loadData(true);
  timerId = window.setInterval(() => {
    void loadData(false);
  }, 3000);

  return () => {
    isUnmounted = true;
    if (timerId !== null) {
      clearInterval(timerId);
    }
  };
}
