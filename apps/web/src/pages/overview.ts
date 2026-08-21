import { api } from '../api/client';
import { BotStatus } from '@frostagent/proto';
import { formatCount, formatStatus, formatUptime, escapeHtml } from '../utils/formatters';
import { icon } from '../components/icons';

export function mountOverviewPage(container: HTMLElement): () => void {
  let timerId: number | null = null;
  let isUnmounted = false;

  container.innerHTML = `
    <div class="page-container fade-in">
      <div id="overview-loading" class="flex items-center gap-2 text-sm text-muted">
        <span class="spinner"></span>
        <span>加载中...</span>
      </div>
      <div id="overview-error" style="display: none;"></div>
      <div id="overview-content" style="display: none;" class="flex flex-col gap-6"></div>
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
            <article class="card">
              <div class="card-header" style="padding-bottom: 0.5rem;">
                <div class="flex items-center gap-2">
                  <span class="text-primary">${icon('construction')}</span>
                  <h3 class="card-title text-sm">${escapeHtml(tool.name)}</h3>
                </div>
              </div>
              <div class="card-content text-xs text-muted leading-relaxed">
                ${escapeHtml(tool.description || '无描述')}
              </div>
            </article>
          `,
              )
              .join('')
          : `
          <div class="card p-6 text-center text-muted col-span-3">
            暂无已注册工具。
          </div>
        `;

      contentEl.innerHTML = `
        <header class="flex flex-col gap-3">
          <h1 class="page-title text-3xl font-bold">
            你好👋！我是 ${escapeHtml(botName)}
          </h1>
          <div class="flex flex-wrap gap-2">
            <span class="badge ${statusBadgeClass}">
              ${icon('sensors', 'sm')}
              ${escapeHtml(formatStatus(data.status))}
            </span>
            <span class="badge badge-outline">
              ${icon('schedule', 'sm')}
              ${escapeHtml(formatUptime(data.uptimeSeconds))}
            </span>
            <span class="badge badge-outline">
              ${icon('deployed_code', 'sm')}
              后端版本 ${escapeHtml(version)}
            </span>
          </div>
        </header>

        <section class="grid grid-cols-1 sm:grid-cols-3 gap-4" aria-label="Bot statistics">
          <article class="card p-4">
            <div class="flex items-center gap-3">
              <div class="flex items-center justify-center w-10 h-10 rounded-lg bg-muted text-primary">
                ${icon('mark_chat_read')}
              </div>
              <div>
                <p class="text-xs text-muted">处理消息</p>
                <p class="text-xl font-bold">${escapeHtml(formatCount(data.totalMessagesProcessed))}</p>
              </div>
            </div>
          </article>

          <article class="card p-4">
            <div class="flex items-center gap-3">
              <div class="flex items-center justify-center w-10 h-10 rounded-lg bg-muted text-primary">
                ${icon('forum')}
              </div>
              <div>
                <p class="text-xs text-muted">活跃会话</p>
                <p class="text-xl font-bold">${escapeHtml(formatCount(data.activeSessions))}</p>
              </div>
            </div>
          </article>

          <article class="card p-4">
            <div class="flex items-center gap-3">
              <div class="flex items-center justify-center w-10 h-10 rounded-lg bg-muted text-primary">
                ${icon('psychology')}
              </div>
              <div class="min-w-0">
                <p class="text-xs text-muted">当前模型</p>
                <p class="text-xl font-bold truncate" title="${escapeHtml(data.currentModel || '-')}">
                  ${escapeHtml(data.currentModel || '-')}
                </p>
              </div>
            </div>
          </article>
        </section>

        <section class="flex flex-col gap-3">
          <h2 class="text-lg font-semibold">
            我可以使用 ${data.tools.length} 个工具
          </h2>
          <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            ${toolsHtml}
          </div>
        </section>
      `;
    } catch (err) {
      if (isUnmounted) return;
      loadingEl.style.display = 'none';
      errorEl.style.display = 'block';
      errorEl.innerHTML = `
        <div class="card p-4 border-destructive text-destructive flex items-center gap-2">
          ${icon('error')}
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
