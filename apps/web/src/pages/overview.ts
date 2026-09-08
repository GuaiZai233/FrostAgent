import { instanceState, instanceRequest } from '../instance-state';
import { openQuickConfig } from '../components/instances';
import { toast } from '../components/toast';
import { createInstanceAPI } from '../api/client';
import { BotStatus } from '@frostagent/proto';
import { formatCount, formatStatus, formatUptime, escapeHtml } from '../utils/formatters';
import { icon } from '../components/icons';
import { instanceWebSocketURL } from '../utils/websocket-url';
import { copyToClipboard } from '../utils/clipboard';

export function mountOverviewPage(container: HTMLElement): () => void {
 const api = createInstanceAPI();
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
      const version = data.version || '-';
      const oneBotURL = instanceWebSocketURL(
        data.wsListenAddr,
        instance?.id || '',
        'onebot',
        window.location.origin,
      );
      const astrBotURL = instanceWebSocketURL(
        data.wsListenAddr,
        instance?.id || '',
        'astrbot',
        window.location.origin,
      );

      const toolsHtml =
        data.tools.length > 0
          ? data.tools
              .map(
                (tool) => `
            <article class="card p-3.5 flex flex-col gap-2 hover-bg transition-colors">
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
 <button class="btn btn-outline btn-sm" id="quick-config">快速配置</button>
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
          <p class="text-xs text-muted">实例：${escapeHtml(instance?.name || '-')} <span class="font-mono">(${escapeHtml(instance?.id || '-')})</span></p>
          <p class="text-xs text-muted font-mono break-all">OneBot: ${escapeHtml(oneBotURL)} <a href="#" role="button" class="text-primary hover:underline ml-1.5 cursor-pointer font-sans select-none" data-copy-url="${escapeHtml(oneBotURL)}">复制</a><br>AstrBot: ${escapeHtml(astrBotURL)} <a href="#" role="button" class="text-primary hover:underline ml-1.5 cursor-pointer font-sans select-none" data-copy-url="${escapeHtml(astrBotURL)}">复制</a></p>
        </header>

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
 ${instance?.restart_required ? `<p class="text-sm text-warning">需要重启实例后生效</p>` : ""}

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
      contentEl.querySelector("#quick-config")!.addEventListener("click",()=>void openQuickConfig());
      contentEl.querySelectorAll<HTMLAnchorElement>('[data-copy-url]').forEach((el) => {
        el.addEventListener('click', async (e) => {
          e.preventDefault();
          const target = el.dataset.copyUrl;
          if (!target) return;
          const ok = await copyToClipboard(target);
          if (ok) {
            toast.success('已复制到剪贴板');
          } else {
            toast.error('复制失败');
          }
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
