import { instanceState, instanceRequest } from '../instance-state';
import { openDialog } from '../components/dialog';
import { toast } from '../components/toast';
import { createInstanceAPI } from '../api/client';
import { BotStatus } from '@frostagent/proto';
import { formatCount, formatStatus, formatUptime, escapeHtml } from '../utils/formatters';
import { icon } from '../components/icons';
import { renderOverviewTools, renderOverviewToolsAction, type ToolCapability } from './overview-tools';
import { renderTokenUsage } from '../components/token-usage';
import { mountVersionControl } from '../components/version-panel';
import { mountInstanceRequired } from '../components/instance-required';
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

      const botName = data.botName || '助手';
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
      const toolsHadFocus = contentEl.querySelector('#overview-tools')?.contains(document.activeElement);
      const focusedToolID = toolsHadFocus ? (document.activeElement as HTMLElement)?.id : '';
      const focusedToolToggle = toolsHadFocus ? (document.activeElement as HTMLElement)?.dataset.toolToggle : undefined;
      const focusedToolConfig = toolsHadFocus ? (document.activeElement as HTMLElement)?.dataset.toolConfig : undefined;

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
        <section id="overview-tools" class="flex flex-col gap-3">
          <div class="flex items-center justify-between gap-3">
            <h2 class="text-sm font-semibold text-foreground flex items-center gap-1.5">
              <span>已注册工具能力</span>
              <span class="badge badge-outline text-xs">${data.tools.length}</span>
            </h2>
            ${renderOverviewToolsAction(data.tools.length)}
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
        const tool = tools.find((item) => item.name === toggle.dataset.toolToggle);
        if (enabled && tool?.available === false) {
          toggle.checked = false;
          if (!isUnmounted) toast.error('工具启用失败：请先点击卡片后进行配置！');
          return;
        }
        toolRevision++;
        toolUpdates++;
        root.querySelectorAll<HTMLInputElement>('[data-tool-toggle]').forEach((other) => { other.disabled = true; });
        try {
          await api.setToolEnabled(toggle.dataset.toolToggle!, enabled);
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
      const providers = cfg.providers.map((provider) => `<label class="flex items-center gap-2 text-sm"><input type="radio" name="balance-query-provider" value="${escapeHtml(provider.id)}" data-available="${provider.available}" ${provider.id === cfg.providerId ? 'checked' : ''} ${provider.available ? '' : 'disabled'}><span>${escapeHtml(provider.displayName || provider.id)}${provider.available ? '' : '（未启用或未配置 API Key）'}</span></label>`).join('');
      const missingProvider = cfg.providerId && !cfg.providers.some((provider) => provider.id === cfg.providerId);
      await openDialog({
        title: '配置 balance_query',
        description: 'FrostAgent 使用已保存的请求查询额度，模型不能传入或修改请求参数。',
        maxWidth: '42rem',
        canDismiss: () => !saving,
        bodyHtml: `<form id="balance-query-form" class="flex flex-col gap-4">
          <label class="form-group"><span class="form-label">curl 请求模板</span><textarea id="balance-query-curl" class="textarea font-mono" rows="7" required>${escapeHtml(cfg.curl)}</textarea></label>
          <p class="text-xs text-muted">用 {{API_KEY}} 代替密钥，支持 HTTP/HTTPS（含内网）、GET/POST、-X、-H、-d、--data-raw、--data-binary、-G 和 --url。不会执行 shell、读取文件或跟随重定向。</p>
          <label class="flex items-center gap-2 text-sm"><input id="balance-query-reuse-key" type="checkbox" class="checkbox" ${cfg.providerId ? 'checked' : ''}>复用已有配置的 API Key</label>
          <fieldset id="balance-query-providers" class="flex flex-col gap-2" ${cfg.providerId ? '' : 'hidden'}><legend class="form-label">选择一个 Provider</legend>${providers || '<p class="text-xs text-muted">暂无 Provider，请先在供应商页面配置 API Key。</p>'}${missingProvider ? '<p class="text-xs text-destructive">原 Provider 已不可用，请重新选择。</p>' : ''}<p class="text-xs text-muted">直接使用所选 Provider 当前已发布的 API Key，无需重复填写。</p></fieldset>
          <div id="balance-query-manual-key" class="form-group" ${cfg.providerId ? 'hidden' : ''}><label class="form-label" for="balance-query-key">API Key</label><div class="flex items-center gap-2"><input id="balance-query-key" class="input flex-1" type="password" autocomplete="new-password" placeholder="${cfg.apiKeyConfigured && !cfg.providerId ? '已配置；留空保留原密钥' : '填写查询接口的 API Key'}"><button type="button" id="balance-query-clear-key" class="btn btn-outline btn-sm shrink-0" ${cfg.apiKeyConfigured && !cfg.providerId ? '' : 'disabled'}>清除</button></div><p id="balance-query-key-hint" class="text-xs text-muted">${cfg.apiKeyConfigured && !cfg.providerId ? '已配置。留空保存会保留原密钥；点击「清除」后保存会删除密钥。' : '填写查询接口的 API Key。'}</p></div>
          <p class="text-xs text-muted">凭据只用于后端请求，不加入模型工具定义和调用参数。模型收到接口的原始响应正文；包含密钥的响应会被拦截。</p>
          <p id="balance-query-error" role="alert" class="text-sm text-destructive"></p>
        </form>`,
        footerHtml: '<button type="button" class="btn btn-outline dialog-close-btn">取消</button><button type="submit" form="balance-query-form" id="balance-query-save" class="btn btn-primary">保存</button>',
        onMount: (dialog, close) => {
          closeConfigDialog = close;
          const key = dialog.querySelector<HTMLInputElement>('#balance-query-key')!;
          const clearKeyButton = dialog.querySelector<HTMLButtonElement>('#balance-query-clear-key')!;
          const keyHint = dialog.querySelector<HTMLElement>('#balance-query-key-hint')!;
          const reuseKey = dialog.querySelector<HTMLInputElement>('#balance-query-reuse-key')!;
          const providerGroup = dialog.querySelector<HTMLFieldSetElement>('#balance-query-providers')!;
          const providerInputs = providerGroup.querySelectorAll<HTMLInputElement>('input');
          const manualKey = dialog.querySelector<HTMLElement>('#balance-query-manual-key')!;
          const curl = dialog.querySelector<HTMLTextAreaElement>('#balance-query-curl')!;
          const save = dialog.querySelector<HTMLButtonElement>('#balance-query-save')!;
          let clearKey = false;
          const syncCredentials = () => {
            providerGroup.hidden = !reuseKey.checked;
            manualKey.hidden = reuseKey.checked;
            key.disabled = saving || reuseKey.checked;
            key.required = !reuseKey.checked && !clearKey && (!cfg.apiKeyConfigured || !!cfg.providerId);
            clearKeyButton.disabled = saving || reuseKey.checked || !cfg.apiKeyConfigured || !!cfg.providerId || clearKey;
            keyHint.textContent = clearKey
              ? '保存后将清除已配置的 API Key。'
              : cfg.apiKeyConfigured && !cfg.providerId
              ? '已配置。留空保存会保留原密钥；点击「清除」后保存会删除密钥。'
              : '填写查询接口的 API Key。';
            reuseKey.disabled = saving;
            providerInputs.forEach((input) => { input.disabled = saving || input.dataset.available !== 'true'; });
          };
          const setSaving = (pending: boolean) => {
            saving = pending;
            save.disabled = pending;
            save.textContent = pending ? '保存中...' : '保存';
            dialog.querySelectorAll<HTMLButtonElement>('.dialog-close-btn').forEach((button) => { button.disabled = pending; });
            curl.disabled = pending;
            syncCredentials();
          };
          reuseKey.onchange = () => { clearKey = false; syncCredentials(); };
          clearKeyButton.onclick = () => {
            if (saving || reuseKey.checked || !cfg.apiKeyConfigured || clearKey) return;
            clearKey = true;
            key.value = '';
            syncCredentials();
            key.focus();
          };
          key.oninput = () => {
            if (!clearKey) return;
            clearKey = false;
            syncCredentials();
          };
          syncCredentials();
          dialog.querySelector<HTMLFormElement>('#balance-query-form')!.onsubmit = async (event) => {
            event.preventDefault();
            if (saving) return;
            const error = dialog.querySelector<HTMLElement>('#balance-query-error')!;
            error.textContent = '';
            const provider = providerGroup.querySelector<HTMLInputElement>('input:checked:not(:disabled)');
            if (reuseKey.checked && !provider) { error.textContent = '请选择已启用且已配置 API Key 的 Provider'; return; }
            if (!reuseKey.checked && key.required && !key.value) { error.textContent = '请填写 API Key'; return; }
            const clear = !reuseKey.checked && clearKey;
            setSaving(true);
            try {
              await api.saveBalanceQueryConfig(curl.value, reuseKey.checked ? '' : key.value, reuseKey.checked ? provider!.value : '', clear);
              if (isUnmounted || !dialog.isConnected) return;
              toolRevision++;
              clearKey = false;
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
    cleanupVersion();
    closeConfigDialog?.();
    closeToolsDialog?.();
    if (timerId !== null) {
      clearInterval(timerId);
    }
  };
}
