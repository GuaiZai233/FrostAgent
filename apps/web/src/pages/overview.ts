import { instanceState, instanceRequest } from '../instance-state';
import { openDialog } from '../components/dialog';
import { toast } from '../components/toast';
import { createInstanceAPI } from '../api/client';
import { BotStatus } from '@frostagent/proto';
import { formatCount, formatStatus, formatUptime, escapeHtml } from '../utils/formatters';
import { icon } from '../components/icons';
import { renderOverviewTools, type ToolCapability } from './overview-tools';
import '../styles/overview.css';

export function mountOverviewPage(container: HTMLElement): () => void {
  const api = createInstanceAPI();
  let timerId: number | null = null;
  let isUnmounted = false;
  let loading = false;
  let toolUpdates = 0;
  let toolRevision = 0;
  let closeToolsDialog: (() => void) | undefined;
  let closeConfigDialog: (() => void) | undefined;
  let configOpening = false;

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
    if (isUnmounted || loading || toolUpdates > 0 || closeToolsDialog || configOpening) return;
    loading = true;
    const revision = toolRevision;
    if (showLoading && contentEl.children.length === 0) {
      loadingEl.style.display = 'flex';
    }

    try {
      const [data] = await Promise.all([api.getOverview(),instanceState.refresh()]);
 const instance = instanceState.selected;
      if (isUnmounted || revision !== toolRevision) return;

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

      const toolsScroll = contentEl.querySelector<HTMLElement>('#overview-tools-strip')?.scrollLeft || 0;
      const toolsHadFocus = contentEl.querySelector('#overview-tools-strip')?.contains(document.activeElement);
      const focusedToolID = toolsHadFocus ? (document.activeElement as HTMLElement)?.id : '';
      const focusedToolToggle = toolsHadFocus ? (document.activeElement as HTMLElement)?.dataset.toolToggle : undefined;
      const focusedToolConfig = toolsHadFocus ? (document.activeElement as HTMLElement)?.dataset.toolConfig : undefined;

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
                ${escapeHtml(version)}
              </span>
            </div>
          </div>
          <p class="text-xs text-muted">实例：${escapeHtml(instance?.name || '-')} <span class="font-mono">(${escapeHtml(instance?.id || '-')})</span></p>
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
      contentEl.querySelector<HTMLElement>('#overview-tools-strip')!.scrollLeft = toolsScroll;
      bindToolControls(contentEl, data.tools);
      if (focusedToolID) contentEl.querySelector<HTMLElement>(`#${focusedToolID}`)?.focus({ preventScroll: true });
      contentEl.querySelectorAll<HTMLElement>('[data-tool-toggle], [data-tool-config]').forEach((control) => {
        if ((focusedToolToggle && control.dataset.toolToggle === focusedToolToggle) || (focusedToolConfig && control.dataset.toolConfig === focusedToolConfig)) control.focus({ preventScroll: true });
      });
      contentEl.querySelector('#overview-all-tools')?.addEventListener('click', () => {
        if (closeToolsDialog) return;
        void openDialog({
          title: '已注册工具能力',
          description: `共 ${data.tools.length} 个工具`,
          maxWidth: '64rem',
          bodyHtml: `<div class="overview-all-tools">${renderOverviewTools(data.tools, false)}</div>`,
          onMount: (dialog, close) => {
            closeToolsDialog = close;
            bindToolControls(dialog, data.tools);
          },
          onClose: () => { closeToolsDialog = undefined; void loadData(); },
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

  function bindToolControls(root: HTMLElement, tools: ToolCapability[]) {
    root.querySelectorAll<HTMLButtonElement>('[data-tool-config]').forEach((button) => {
      button.addEventListener('click', () => {
        if (button.dataset.toolConfig === 'balance_query') void openBalanceConfig();
      });
    });
    root.querySelectorAll<HTMLInputElement>('[data-tool-toggle]').forEach((toggle) => {
      toggle.addEventListener('change', async () => {
        const enabled = toggle.checked;
        toolRevision++;
        toolUpdates++;
        root.querySelectorAll<HTMLInputElement>('[data-tool-toggle]').forEach((other) => { other.disabled = true; });
        try {
          await api.setToolEnabled(toggle.dataset.toolToggle!, enabled);
          const tool = tools.find((item) => item.name === toggle.dataset.toolToggle);
          if (tool) tool.enabled = enabled;
          if (!isUnmounted) {
            container.querySelectorAll<HTMLInputElement>('[data-tool-toggle]').forEach((other) => {
              if (other.dataset.toolToggle === toggle.dataset.toolToggle) other.checked = enabled;
            });
          }
        } catch (err) {
          toggle.checked = !enabled;
          if (!isUnmounted) toast.error(String(err));
        } finally {
          toolUpdates--;
          root.querySelectorAll<HTMLInputElement>('[data-tool-toggle]').forEach((other) => { other.disabled = false; });
        }
      });
    });
  }

  async function openBalanceConfig() {
    if (isUnmounted || configOpening || toolUpdates > 0) return;
    configOpening = true;
    try {
      const cfg = await api.getBalanceQueryConfig();
      if (isUnmounted) return;
      let saving = false;
      await openDialog({
        title: '配置 balance_query',
        description: 'FrostAgent 使用已保存的请求查询额度，模型不能传入或修改请求参数。',
        maxWidth: '42rem',
        canDismiss: () => !saving,
        bodyHtml: `<form id="balance-query-form" class="flex flex-col gap-4">
          <label class="form-group"><span class="form-label">curl 请求模板</span><textarea id="balance-query-curl" class="textarea font-mono" rows="7" required>${escapeHtml(cfg.curl)}</textarea></label>
          <p class="text-xs text-muted">用 {{API_KEY}} 代替密钥，支持 HTTP/HTTPS（含内网）、GET/POST、-X、-H、-d、--data-raw、--data-binary、-G 和 --url。不会执行 shell、读取文件或跟随重定向。</p>
          <label class="form-group"><span class="form-label">API key</span><input id="balance-query-key" class="input" type="password" autocomplete="new-password" placeholder="${cfg.apiKeyConfigured ? '已配置；留空保留原密钥' : '填写查询接口的 API key'}"></label>
          <label class="flex items-center gap-2 text-sm"><input id="balance-query-clear-key" type="checkbox" class="checkbox">清除已保存的 API key</label>
          <p class="text-xs text-muted">凭据只用于后端请求，不加入模型工具定义和调用参数。模型收到接口的原始响应正文；包含密钥的响应会被拦截。</p>
          <p id="balance-query-error" role="alert" class="text-sm text-destructive"></p>
        </form>`,
        footerHtml: '<button type="button" class="btn btn-outline dialog-close-btn">取消</button><button type="submit" form="balance-query-form" id="balance-query-save" class="btn btn-primary">保存</button>',
        onMount: (dialog, close) => {
          closeConfigDialog = close;
          const key = dialog.querySelector<HTMLInputElement>('#balance-query-key')!;
          const clearKey = dialog.querySelector<HTMLInputElement>('#balance-query-clear-key')!;
          const curl = dialog.querySelector<HTMLTextAreaElement>('#balance-query-curl')!;
          const save = dialog.querySelector<HTMLButtonElement>('#balance-query-save')!;
          const setSaving = (pending: boolean) => {
            saving = pending;
            save.disabled = pending;
            save.textContent = pending ? '保存中...' : '保存';
            dialog.querySelectorAll<HTMLButtonElement>('.dialog-close-btn').forEach((button) => { button.disabled = pending; });
            curl.disabled = pending;
            clearKey.disabled = pending;
            key.disabled = pending || clearKey.checked;
          };
          clearKey.onchange = () => { key.disabled = clearKey.checked; if (clearKey.checked) key.value = ''; };
          dialog.querySelector<HTMLFormElement>('#balance-query-form')!.onsubmit = async (event) => {
            event.preventDefault();
            if (saving) return;
            setSaving(true);
            const error = dialog.querySelector<HTMLElement>('#balance-query-error')!;
            error.textContent = '';
            try {
              await api.saveBalanceQueryConfig(curl.value, key.value, clearKey.checked);
              if (isUnmounted || !dialog.isConnected) return;
              toolRevision++;
              key.value = '';
              close();
              closeToolsDialog?.();
              toast.success('额度查询配置已保存');
            } catch (err) {
              if (dialog.isConnected && !isUnmounted) error.textContent = String(err);
            } finally { setSaving(false); }
          };
        },
        onClose: () => { closeConfigDialog = undefined; },
      });
    } catch (err) {
      if (!isUnmounted) toast.error(String(err));
    } finally {
      configOpening = false;
      if (!isUnmounted) void loadData();
    }
  }

  void loadData(true);
  timerId = window.setInterval(() => {
    void loadData(false);
  }, 3000);

  return () => {
    isUnmounted = true;
    closeConfigDialog?.();
    closeToolsDialog?.();
    if (timerId !== null) {
      clearInterval(timerId);
    }
  };
}
