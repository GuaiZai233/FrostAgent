import { instanceState, instanceRequest } from '../instance-state';
import { openDialog } from '../components/dialog';
import { toast } from '../components/toast';
import { createInstanceAPI } from '../api/client';
import { BotStatus } from '@frostagent/proto';
import { formatCount, formatStatus, formatUptime, escapeHtml } from '../utils/formatters';
import { icon } from '../components/icons';
import { renderOverviewTools } from './overview-tools';
import { renderTokenUsage } from '../components/token-usage';
import { mountVersionControl } from '../components/version-panel';
import { mountInstanceRequired } from '../components/instance-required';
import '../styles/overview.css';

export function mountOverviewPage(container: HTMLElement): () => void {
  const api = createInstanceAPI();
  let timerId: number | null = null;
  let isUnmounted = false;
  let loading = false;
  let closeToolsDialog: (() => void) | undefined;

  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="flex flex-col gap-2 pb-1">
        <div class="flex items-center justify-between gap-3 flex-wrap">
          <h1 id="overview-greeting" class="page-title text-xl font-bold" hidden></h1>
          <div class="flex flex-wrap gap-1.5 items-center">
            <span id="overview-runtime" class="contents"></span>
            <div id="overview-version"></div>
          </div>
        </div>
        <p id="overview-instance" class="text-xs text-muted" hidden></p>
      </header>
      <div id="overview-loading" class="flex items-center gap-2 text-xs text-muted">
        <span class="spinner"></span>
        <span>加载概览数据...</span>
      </div>
      <div id="overview-error" style="display: none;"></div>
      <div id="overview-content" style="display: none;" class="flex flex-col gap-5"></div>
    </div>
  `;

  const cleanupVersion = mountVersionControl(container.querySelector<HTMLElement>('#overview-version')!);
  if (!instanceState.selected) {
    container.querySelector<HTMLElement>('#overview-loading')!.style.display = 'none';
    const content = container.querySelector<HTMLElement>('#overview-content')!;
    content.style.display = 'flex';
    mountInstanceRequired(content);
    return cleanupVersion;
  }

  const loadingEl = container.querySelector<HTMLElement>('#overview-loading')!;
  const errorEl = container.querySelector<HTMLElement>('#overview-error')!;
  const contentEl = container.querySelector<HTMLElement>('#overview-content')!;

  async function loadData(showLoading = false) {
    if (isUnmounted || loading) return;
    loading = true;
    if (showLoading && contentEl.children.length === 0) {
      loadingEl.style.display = 'flex';
    }

    try {
      const [data] = await Promise.all([api.getOverview(),instanceState.refresh()]);
 const instance = instanceState.selected;
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
      const greeting = container.querySelector<HTMLElement>('#overview-greeting')!;
      greeting.textContent = `你好👋！我是 ${botName}`;
      greeting.hidden = false;
      const instanceLabel = container.querySelector<HTMLElement>('#overview-instance')!;
      instanceLabel.innerHTML = `实例：${escapeHtml(instance?.name || '-')} <span class="font-mono">(${escapeHtml(instance?.id || '-')})</span>`;
      instanceLabel.hidden = false;
      container.querySelector<HTMLElement>('#overview-runtime')!.innerHTML = `
        <span class="badge ${statusBadgeClass}">
          ${icon('activity', 'w-3 h-3')}
          ${escapeHtml(formatStatus(data.status))}
        </span>
        <span class="badge badge-outline">
          ${icon('clock', 'w-3 h-3')}
          ${escapeHtml(formatUptime(data.uptimeSeconds))}
        </span>`;

      const toolsScroll = contentEl.querySelector<HTMLElement>('#overview-tools-strip')?.scrollLeft || 0;
      const toolsHadFocus = contentEl.querySelector('#overview-tools-strip')?.contains(document.activeElement);
      const focusedToolID = toolsHadFocus ? (document.activeElement as HTMLElement)?.id : '';

      const usageFocused = document.activeElement?.classList.contains('token-usage-table-wrap');
      const usageScroll = contentEl.querySelector<HTMLElement>('.token-usage-table-wrap')?.scrollLeft || 0;
      contentEl.innerHTML = `
        <!-- KPI Summary Cards -->
        <section class="grid grid-cols-1 instance-kpis gap-3.5" aria-label="Bot statistics">
          <article class="card p-3.5 flex items-center gap-3.5">
            <div class="flex items-center justify-center w-9 h-9 rounded-md bg-muted text-primary flex-shrink-0">
              ${icon('message_square', 'w-4 h-4')}
            </div>
            <div class="min-w-0">
              <p class="text-xs text-muted font-medium">处理消息</p>
              <p class="text-lg font-bold tracking-tight text-foreground">${escapeHtml(formatCount(data.totalMessagesProcessed))}</p>
            </div>
          </article>

          <article class="card p-3.5 flex items-center gap-3.5">
            <div class="flex items-center justify-center w-9 h-9 rounded-md bg-muted text-primary flex-shrink-0">
              ${icon('users', 'w-4 h-4')}
            </div>
            <div class="min-w-0">
              <p class="text-xs text-muted font-medium">活跃会话</p>
              <p class="text-lg font-bold tracking-tight text-foreground">${escapeHtml(formatCount(data.activeSessions))}</p>
            </div>
          </article>

          <article class="card p-3.5 flex items-center gap-3.5">
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
<article class="card p-3.5 flex flex-col gap-2"><p class="text-xs text-muted font-medium">是否启用</p><label class="instance-toggle"><input type="checkbox" role="switch" aria-label="是否启用" id="instance-enabled" ${instance?.enabled ? "checked" : ""}><span></span></label></article>
 </section>
 ${instance?.error ? `<p class="text-sm text-destructive">${escapeHtml(instance.error)}</p>` : ""}

        <!-- Tools Capability Section -->
        <section class="flex flex-col gap-3">
          <div class="flex items-center justify-between">
            <h2 class="text-sm font-semibold text-foreground flex items-center gap-1.5">
              <span>已注册工具能力</span>
              <span class="badge badge-outline text-xs">${data.tools.length}</span>
            </h2>
          </div>
          <div id="overview-tools-strip" class="overview-tools-strip" tabindex="0" role="region" aria-label="已注册工具能力，横向滚动查看">
            ${renderOverviewTools(data.tools)}
          </div>
        </section>

        ${renderTokenUsage(data.tokenUsage)}

        <section class="flex flex-col gap-3" aria-labelledby="overview-resources-title">
          <h2 id="overview-resources-title" class="text-sm font-semibold text-foreground">资源</h2>
          <div class="overview-resources">
            <a class="card p-4 overview-resource hover-bg text-foreground" href="https://github.com/GuaiZai233/FrostAgent" target="_blank" rel="noopener noreferrer">
              ${icon('github', 'w-5 h-5')}<span class="text-sm font-semibold">GitHub</span>${icon('chevron_right')}
            </a>
            <a class="card p-4 overview-resource hover-bg text-foreground" href="https://frostagent.guaitech.org/docs" target="_blank" rel="noopener noreferrer">
              ${icon('book_open', 'w-5 h-5')}<span class="text-sm font-semibold">文档</span>${icon('chevron_right')}
            </a>
          </div>
        </section>
      `;
      const usageTable = contentEl.querySelector<HTMLElement>('.token-usage-table-wrap');
      if (usageTable) {
        usageTable.scrollLeft = usageScroll;
        if (usageFocused) usageTable.focus({ preventScroll: true });
      }
      contentEl.querySelector<HTMLElement>('#overview-tools-strip')!.scrollLeft = toolsScroll;
      if (focusedToolID) contentEl.querySelector<HTMLElement>(`#${focusedToolID}`)?.focus({ preventScroll: true });
      contentEl.querySelector('#overview-all-tools')?.addEventListener('click', () => {
        if (closeToolsDialog) return;
        void openDialog({
          title: '已注册工具能力',
          description: `共 ${data.tools.length} 个工具`,
          maxWidth: '64rem',
          bodyHtml: `<div class="overview-all-tools">${renderOverviewTools(data.tools, false)}</div>`,
          onMount: (_dialog, close) => { closeToolsDialog = close; },
          onClose: () => { closeToolsDialog = undefined; },
        });
      });
      contentEl.querySelector<HTMLInputElement>("#instance-enabled")!.onchange = async (event) => {
 const enabled=(event.target as HTMLInputElement).checked;
 try {if(instance)await instanceRequest("/"+instance.id+"/enable",{enabled});}catch(err){toast.error(String(err));}
 if(!isUnmounted)void loadData();
 };
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
    } finally {
      loading = false;
    }
  }

  void loadData(true);
  timerId = window.setInterval(() => {
    void loadData(false);
  }, 3000);

  return () => {
    isUnmounted = true;
    cleanupVersion();
    closeToolsDialog?.();
    if (timerId !== null) {
      clearInterval(timerId);
    }
  };
}
