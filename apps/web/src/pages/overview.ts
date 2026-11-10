import { instanceState, instanceRequest } from '../instance-state';
import { openDialog } from '../components/dialog';
import { toast } from '../components/toast';
import { confirmDialog } from '../components/confirm';
import { createInstanceAPI } from '../api/client';
import { BotStatus } from '@frostagent/proto';
import { formatCount, formatStatus, formatUptime, escapeHtml } from '../utils/formatters';
import { balanceQueryOrigin, parseBalanceQueryTarget, type BalanceQueryTarget } from '../utils/balance-query-request';
import { icon } from '../components/icons';
import { renderOverviewTools, renderOverviewToolsAction, type ToolCapability } from './overview-tools';
import { renderTokenUsage } from '../components/token-usage';
import { mountVersionControl } from '../components/version-panel';
import { mountInstanceRequired } from '../components/instance-required';
import '../styles/overview.css';

// Preset curl templates for balance_query. The backend only substitutes {{API_KEY}};
// NewAPI's host is a placeholder the user must replace with their own address.
// path: the New API endpoint path, appended to a provider's base origin when the endpoint is reused.
const balanceQueryPresets: Record<string, { curl: string; keyPlaceholder: string; keyHint: string; path?: string }> = {
  deepseek: {
    curl: "curl https://api.deepseek.com/user/balance \\\n  -H 'Authorization: Bearer {{API_KEY}}'",
    keyPlaceholder: 'sk-xxx',
    keyHint: '此处填写DeepSeek API Key',
  },
  'newapi-key': {
    curl: "curl https://你的NewAPI地址/api/usage/token/ \\\n  -H 'Authorization: Bearer {{API_KEY}}'",
    keyPlaceholder: 'sk-xxx',
    keyHint: '填写该 Token 的 sk- Key。返回该 Token 的剩余额度；不限额度时 unlimited_quota 为 true。',
    path: '/api/usage/token/',
  },
  'newapi-account': {
    curl: "curl https://你的NewAPI地址/api/user/self \\\n  -H 'Authorization: Bearer {{API_KEY}}'",
    keyPlaceholder: 'nap_xxx',
    keyHint: '在面板安全与访问 -> 创建访问令牌，至少勾选 profile:read 权限。返回账号的 quota 与 used_quota。',
    path: '/api/user/self',
  },
};

function newApiCredentialType(path: string | undefined): 'newapi-key' | 'newapi-account' | '' {
  switch (path?.replace(/\/+$/, '')) {
    case '/api/usage/token': return 'newapi-key';
    case '/api/user/self': return 'newapi-account';
    default: return '';
  }
}

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
        if (enabled && tool?.name === 'balance_query' && tool.available === false) {
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

  // After saving or clearing the balance configuration, return to the 已注册工具能力 window if it was open,
  // reopened with fresh data so its status reflects the change. Otherwise the user stays on the main page.
  async function returnToToolsDialog(hadToolsDialog: boolean) {
    if (!hadToolsDialog || isUnmounted) return;
    closeToolsDialog?.();
    await loadData();
    if (isUnmounted) return;
    document.querySelector<HTMLButtonElement>('#overview-all-tools')?.click();
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
          <div class="form-group"><label class="form-label" for="balance-query-curl">curl 请求模板</label>
            <select id="balance-query-preset" class="select" aria-label="预设模板"><option value="" selected>自定义</option><option value="deepseek">DeepSeek</option><option value="newapi-key">New API 当前 Key 额度</option><option value="newapi-account">New API 账号余额</option></select>
            <textarea id="balance-query-curl" class="textarea font-mono" rows="7" required>${escapeHtml(cfg.curl)}</textarea></div>
          <p class="text-xs text-muted">支持 HTTP/HTTPS（含内网）、GET/POST、-X、-H、-d、--data-raw、--data-binary、-G 和 --url。</p>
          <label id="balance-query-endpoint-row" class="flex items-center gap-2 text-sm" hidden><input id="balance-query-use-endpoint" type="checkbox" class="checkbox">复用现有 Provider 端点</label>
          <label class="flex items-center gap-2 text-sm"><input id="balance-query-reuse-key" type="checkbox" class="checkbox" ${cfg.providerId ? 'checked' : ''}>复用已有配置的 Key</label>
          <fieldset id="balance-query-providers" class="flex flex-col gap-2" ${cfg.providerId ? '' : 'hidden'}><legend class="form-label">选择一个 Provider</legend>${providers || '<p class="text-xs text-muted">暂无 Provider，请先在供应商页面配置 API Key。</p>'}${missingProvider ? '<p class="text-xs text-destructive">原 Provider 已不可用，请重新选择。</p>' : ''}<p class="text-xs text-muted">直接使用所选 Provider 当前已发布的 API Key，无需重复填写。</p></fieldset>
          <div id="balance-query-manual-key" class="form-group" ${cfg.providerId ? 'hidden' : ''}><label class="form-label" for="balance-query-key">Key</label><div class="flex items-center gap-2"><input id="balance-query-key" class="input flex-1" type="password" autocomplete="new-password" placeholder="${cfg.apiKeyConfigured && !cfg.providerId ? '已配置；留空保留原密钥' : '填写查询接口的 API Key'}"></div><p id="balance-query-key-hint" class="text-xs text-muted">${cfg.apiKeyConfigured && !cfg.providerId ? '已配置。留空保存会保留原密钥。' : '填写查询接口的 Key。'}</p><p id="balance-query-preset-hint" class="text-xs text-muted" hidden></p></div>
          <div id="balance-query-quota" class="form-group" hidden>
            <label class="flex items-center gap-2 text-sm"><input id="balance-query-quota-enabled" type="checkbox" class="checkbox" ${cfg.quotaPerUnit > 0 ? 'checked' : ''}>内部 Quota 转换</label>
            <div id="balance-query-quota-ratio-row" class="flex flex-col gap-2" ${cfg.quotaPerUnit > 0 ? '' : 'hidden'}>
              <div class="flex items-center gap-2 flex-wrap"><button type="button" id="balance-query-fetch-quota" class="btn btn-outline btn-sm">从远端 API 获取</button><span class="text-xs text-muted">获取中转站的配额换算配置，请求路径：/api/status</span></div>
              <label class="flex items-center gap-2 text-sm"><span class="shrink-0">quota_per_unit：每美元对应的额度单位</span><input id="balance-query-quota-ratio" class="input font-mono" type="number" min="0" step="any" value="${cfg.quotaPerUnit > 0 ? cfg.quotaPerUnit : 500000}" style="width:10rem"></label>
              <label class="flex items-center gap-2 text-sm"><span class="shrink-0">usd_exchange_rate：美元兑人民币的展示汇率</span><input id="balance-query-usd-rate" class="input font-mono" type="number" min="0" step="any" value="${cfg.usdExchangeRate > 0 ? cfg.usdExchangeRate : 7.3}" style="width:10rem"></label>
              <label class="flex items-center gap-2 text-sm"><span class="shrink-0">quota_display_type：配额展示的币种</span><input id="balance-query-display-type" class="input font-mono" type="text" value="${escapeHtml(cfg.quotaDisplayType || 'USD')}" style="width:10rem"></label>
              <label class="flex items-center gap-2 text-sm"><span class="shrink-0">custom_currency_symbol：CUSTOM 时的符号</span><input id="balance-query-custom-symbol" class="input font-mono" type="text" value="${escapeHtml(cfg.customCurrencySymbol || '')}" style="width:10rem"></label>
              <label class="flex items-center gap-2 text-sm"><span class="shrink-0">custom_currency_exchange_rate：CUSTOM 时的换算汇率</span><input id="balance-query-custom-rate" class="input font-mono" type="number" min="0" step="any" value="${cfg.customCurrencyExchangeRate > 0 ? cfg.customCurrencyExchangeRate : 1}" style="width:10rem"></label>
            </div>
            <p class="text-xs text-muted">开启后，New API 返回的内部 quota 字段会按该比例换算为可读性高的格式。</p>
          </div>
          <p class="text-xs text-muted">凭据只用于后端请求，不传递给模型。</p>
          <pre id="balance-query-test-result" class="text-xs font-mono whitespace-pre-wrap break-all p-2 rounded bg-muted border border-border overflow-auto" style="max-height:14rem" hidden></pre>
          <p id="balance-query-error" role="alert" class="text-sm text-destructive"></p>
        </form>`,
        footerHtml: `<button type="button" id="balance-query-clear-config" class="btn btn-ghost text-destructive" style="margin-right:auto">${icon('trash', 'w-3.5 h-3.5')} 清除配置</button><button type="button" id="balance-query-test" class="btn btn-outline">测试连接</button><button type="button" class="btn btn-outline dialog-close-btn">取消</button><button type="submit" form="balance-query-form" id="balance-query-save" class="btn btn-primary">保存</button>`,
        onMount: (dialog, close) => {
          closeConfigDialog = close;
          const key = dialog.querySelector<HTMLInputElement>('#balance-query-key')!;
          const keyHint = dialog.querySelector<HTMLElement>('#balance-query-key-hint')!;
          const reuseKey = dialog.querySelector<HTMLInputElement>('#balance-query-reuse-key')!;
          const providerGroup = dialog.querySelector<HTMLFieldSetElement>('#balance-query-providers')!;
          const providerInputs = providerGroup.querySelectorAll<HTMLInputElement>('input');
          const manualKey = dialog.querySelector<HTMLElement>('#balance-query-manual-key')!;
          const curl = dialog.querySelector<HTMLTextAreaElement>('#balance-query-curl')!;
          const save = dialog.querySelector<HTMLButtonElement>('#balance-query-save')!;
          const preset = dialog.querySelector<HTMLSelectElement>('#balance-query-preset')!;
          const presetHint = dialog.querySelector<HTMLElement>('#balance-query-preset-hint')!;
          const defaultKeyPlaceholder = key.placeholder;
          const endpointRow = dialog.querySelector<HTMLElement>('#balance-query-endpoint-row')!;
          const useEndpoint = dialog.querySelector<HTMLInputElement>('#balance-query-use-endpoint')!;
          // New API mode is remembered separately from the temporary preset selector, so reopening a saved New API
          // template, editing its placeholder host, or saving again keeps the New API fields and values.
          let newApiMode = !!cfg.newapi;
          preset.onchange = () => {
            if (saving) return;
            const selected = balanceQueryPresets[preset.value];
            if (selected) {
              curl.value = selected.curl;
              newApiMode = preset.value === "newapi-key" || preset.value === "newapi-account";
            }
            key.placeholder = selected ? selected.keyPlaceholder : defaultKeyPlaceholder;
            presetHint.textContent = selected ? selected.keyHint : '';
            presetHint.hidden = !selected;
            applyEndpointMode();
            updateRetypeKey();
            applyQuotaBox();
          };
          let retypeKey = false;
          // Provider reuse is only valid for the endpoint and credential type it was chosen for. When the request
          // origin or the New API credential type (sk- token vs nap_ PAT) changes, reuse is withdrawn automatically:
          // the provider selection and manual key are cleared and the user must choose a credential explicitly.
          // The saved provider itself is never modified here.
          // The selector returns to 自定义 after an edit and on reopen, so derive the contract from the request.
          // Keep the last known contract through ordinary-path edits until the user chooses credentials again.
          let credentialType = newApiCredentialType(parseBalanceQueryTarget(cfg.curl)?.path);
          const withdrawProviderReuse = () => {
            if (!reuseKey.checked && !useEndpoint.checked && !providerGroup.querySelector("input:checked")) return;
            reuseKey.checked = false;
            useEndpoint.checked = false;
            providerGroup.querySelectorAll<HTMLInputElement>("input").forEach((input) => { input.checked = false; });
            key.value = "";
            const errorBox = dialog.querySelector<HTMLElement>("#balance-query-error");
            if (errorBox) errorBox.textContent = "";
          };
          const providerOriginChanged = (target: BalanceQueryTarget | null) => {
            const id = providerGroup.querySelector<HTMLInputElement>("input:checked")?.value;
            const provider = cfg.providers.find((item) => item.id === id);
            if (!provider || !provider.baseUrl) return reuseKey.checked;
            return !target || target.origin !== balanceQueryOrigin(provider.baseUrl);
          };
          // Any edit to the stored request template (preset or hand-written) requires the key to be
          // re-entered. The backend separately enforces the endpoint rule with the real curl parser.
          const updateRetypeKey = () => {
            retypeKey = !!cfg.apiKeyConfigured && !cfg.providerId && curl.value.trim() !== cfg.curl.trim();
            const target = parseBalanceQueryTarget(curl.value);
            const nextCredentialType = newApiCredentialType(target?.path);
            // Unknown -> known is a credential-contract transition too. Keep unknown -> unknown edits compatible.
            const contractChanged = nextCredentialType !== credentialType && (nextCredentialType !== '' || credentialType !== '');
            if ((reuseKey.checked || useEndpoint.checked) && (providerOriginChanged(target) || contractChanged)) {
              withdrawProviderReuse();
              applyEndpointMode();
            }
            if (nextCredentialType) credentialType = nextCredentialType;
            syncCredentials();
          };
          curl.addEventListener('input', updateRetypeKey);
          // Hand-editing the template means it no longer matches any preset, so the selector goes back to 自定义.
          curl.addEventListener('input', () => {
            preset.value = '';
            presetHint.hidden = true;
          });
          const clearConfigButton = dialog.querySelector<HTMLButtonElement>('#balance-query-clear-config')!;
          clearConfigButton.onclick = async () => {
            if (saving) return;
            const error = dialog.querySelector<HTMLElement>('#balance-query-error')!;
            error.textContent = '';
            const confirmed = await confirmDialog({
              title: '清除额度查询配置',
              message: '将删除已保存的请求模板与 Key，balance_query 会恢复为未配置状态。确定继续吗？',
              confirmLabel: '清除配置',
              destructive: true,
            });
            if (!confirmed || isUnmounted || !dialog.isConnected) return;
            try {
              await api.clearBalanceQueryConfig();
              if (isUnmounted || !dialog.isConnected) return;
              toolRevision++;
              const hadToolsDialog = !!closeToolsDialog;
              close();
              toast.success('额度查询配置已清除');
              await returnToToolsDialog(hadToolsDialog);
            } catch (err) {
              if (dialog.isConnected && !isUnmounted) error.textContent = String(err);
            }
          };
          const testButton = dialog.querySelector<HTMLButtonElement>('#balance-query-test')!;
          testButton.onclick = async () => {
            if (saving || testButton.disabled) return;
            const error = dialog.querySelector<HTMLElement>('#balance-query-error')!;
            error.textContent = '';
            const provider = providerGroup.querySelector<HTMLInputElement>('input:checked:not(:disabled)');
            const useProvider = reuseKey.checked;
            if (useProvider && !provider) { error.textContent = '请选择已启用且已配置 API Key 的 Provider'; return; }
            testButton.disabled = true;
            testButton.textContent = '测试中...';
            try {
              const result = await api.testBalanceQueryConfig(curl.value, useProvider ? '' : key.value, useProvider ? provider!.value : "", quotaValue(), isNewApi(), quotaExtras());
              if (isUnmounted || !dialog.isConnected) return;
              const resultBox = dialog.querySelector<HTMLElement>('#balance-query-test-result')!;
              if (result.ok) {
                // The response body can be long; show it in a scrollable box instead of a toast.
                resultBox.textContent = result.message;
                resultBox.hidden = false;
              } else {
                resultBox.hidden = true;
                error.textContent = result.message;
              }
            } catch (err) {
              if (dialog.isConnected && !isUnmounted) error.textContent = String(err);
            } finally {
              if (dialog.isConnected) {
                testButton.disabled = false;
                testButton.textContent = '测试连接';
              }
            }
          };
          const syncCredentials = () => {
            providerGroup.hidden = !reuseKey.checked;
            manualKey.hidden = reuseKey.checked;
            key.disabled = saving || reuseKey.checked;
            // Custom templates may leave the key blank (the backend keeps the stored key only for the same endpoint).
            // A chosen preset always needs its own key when none is stored or the template has changed.
            const presetChosen = preset.value !== '';
            key.required = !reuseKey.checked && (!!cfg.providerId || (presetChosen && (!cfg.apiKeyConfigured || retypeKey)));
            keyHint.textContent = cfg.apiKeyConfigured && !cfg.providerId
              ? '已配置。留空保存会保留原密钥。'
              : '填写查询接口的 Key。';
            reuseKey.disabled = saving;
            providerInputs.forEach((input) => { input.disabled = saving || input.dataset.available !== 'true'; });
          };
          const setSaving = (pending: boolean) => {
            saving = pending;
            save.disabled = pending || fetchingQuota;
            save.textContent = pending ? '保存中...' : '保存';
            dialog.querySelectorAll<HTMLButtonElement>('.dialog-close-btn').forEach((button) => { button.disabled = pending; });
            curl.disabled = pending;
            preset.disabled = pending;
            // Every control whose value is captured by the pending save must be locked too, otherwise a change
            // made while the request is in flight would be silently dropped when the dialog closes on success.
            for (const control of [useEndpoint, quotaEnabled, quotaRatio, usdRate, displayType, fetchQuotaButton]) {
              control.disabled = pending;
            }
            if (customSymbol) customSymbol.disabled = pending;
            if (customRate) customRate.disabled = pending;
            syncCredentials();
          };
          reuseKey.onchange = () => {
            credentialType = newApiCredentialType(parseBalanceQueryTarget(curl.value)?.path);
            syncCredentials();
          };
          // New API presets can reuse a provider's origin while keeping the credential choice separate.
          const isNewApi = () => newApiMode;
          // Quota conversion is only offered for New API presets; disabled or hidden means no conversion (0).
          const quotaBox = dialog.querySelector<HTMLElement>('#balance-query-quota')!;
          const quotaEnabled = dialog.querySelector<HTMLInputElement>('#balance-query-quota-enabled')!;
          const quotaRatioRow = dialog.querySelector<HTMLElement>('#balance-query-quota-ratio-row')!;
          const quotaRatio = dialog.querySelector<HTMLInputElement>('#balance-query-quota-ratio')!;
          const quotaValue = (): number => {
            if (quotaBox.hidden || !quotaEnabled.checked) return 0;
            const ratio = Number(quotaRatio.value);
            return Number.isFinite(ratio) && ratio > 0 ? ratio : 0;
          };
          const usdRate = dialog.querySelector<HTMLInputElement>('#balance-query-usd-rate')!;
          const displayType = dialog.querySelector<HTMLInputElement>('#balance-query-display-type')!;
          const customSymbol = dialog.querySelector<HTMLInputElement>('#balance-query-custom-symbol');
          const customRate = dialog.querySelector<HTMLInputElement>('#balance-query-custom-rate');
          const fetchQuotaButton = dialog.querySelector<HTMLButtonElement>('#balance-query-fetch-quota')!;
          let fetchingQuota = false;
          // Exchange rate and display unit are only sent while conversion is on; off means none.
          const quotaExtras = () => {
            if (quotaBox.hidden || !quotaEnabled.checked) return { usd: 0, type: '', symbol: '', customRate: 0 };
            const rate = Number(usdRate.value);
            const custom = Number(customRate?.value ?? '');
            return {
              usd: Number.isFinite(rate) && rate > 0 ? rate : 0,
              type: displayType.value.trim(),
              symbol: customSymbol?.value.trim() ?? '',
              customRate: Number.isFinite(custom) && custom > 0 ? custom : 0,
            };
          };
          const applyQuotaBox = () => {
            quotaBox.hidden = !isNewApi();
            quotaRatioRow.hidden = !quotaEnabled.checked;
          };
          quotaEnabled.onchange = applyQuotaBox;
          // Reads the conversion settings from the template's New API site (/api/status) via the backend.
          fetchQuotaButton.onclick = async () => {
            const error = dialog.querySelector<HTMLElement>('#balance-query-error')!;
            error.textContent = '';
            fetchingQuota = true;
            fetchQuotaButton.disabled = true;
            save.disabled = true;
            try {
              const requestedCurl = curl.value;
              const result = await api.fetchQuotaConfig(requestedCurl);
              if (isUnmounted || !dialog.isConnected) return;
              if (curl.value !== requestedCurl) {
                error.textContent = '请求模板已变化，已丢弃旧地址返回的配置，请重新获取';
                return;
              }
              if (!result.ok) { error.textContent = result.message; return; }
              quotaRatio.value = String(result.quotaPerUnit);
              usdRate.value = String(result.usdExchangeRate || 7.3);
              displayType.value = result.quotaDisplayType || 'USD';
              if (customSymbol) customSymbol.value = result.customCurrencySymbol;
              if (customRate) customRate.value = String(result.customCurrencyExchangeRate);
              toast.success(result.message);
            } catch (err) {
              if (dialog.isConnected && !isUnmounted) error.textContent = String(err);
            } finally {
              fetchingQuota = false;
              if (dialog.isConnected) {
                fetchQuotaButton.disabled = saving;
                save.disabled = saving;
              }
            }
          };
          const selectedProviderOrigin = () => {
            const id = providerGroup.querySelector<HTMLInputElement>('input:checked')?.value ?? '';
            return balanceQueryOrigin(cfg.providers.find((provider) => provider.id === id)?.baseUrl ?? '');
          };
          const applyEndpointMode = () => {
            endpointRow.hidden = !isNewApi();
            if (!isNewApi() && useEndpoint.checked) {
              useEndpoint.checked = false;
            }
            const active = useEndpoint.checked && isNewApi();
            curl.hidden = active;
            curl.required = !active;
            if (active) {
              const origin = selectedProviderOrigin();
              const endpointPreset = balanceQueryPresets[preset.value]
                ?? balanceQueryPresets[newApiCredentialType(parseBalanceQueryTarget(curl.value)?.path) || credentialType || 'newapi-key'];
              if (origin) curl.value = `curl ${origin}${endpointPreset.path} \\\n  -H 'Authorization: Bearer {{API_KEY}}'`;
            }
            syncCredentials();
          };
          useEndpoint.onchange = () => {
            // Turning endpoint reuse off restores the preset's own template, then checks its credential target.
            const presetCurl = balanceQueryPresets[preset.value]?.curl;
            if (!useEndpoint.checked && isNewApi() && presetCurl) curl.value = presetCurl;
            applyEndpointMode();
            updateRetypeKey();
          };
          providerGroup.addEventListener('change', () => {
            if (useEndpoint.checked) applyEndpointMode();
            credentialType = newApiCredentialType(parseBalanceQueryTarget(curl.value)?.path);
          });
          // Apply the restored New API mode to the endpoint and quota sections before the user interacts.
          applyQuotaBox();
          applyEndpointMode();
          updateRetypeKey();
          dialog.querySelector<HTMLFormElement>('#balance-query-form')!.onsubmit = async (event) => {
            event.preventDefault();
            if (saving || fetchingQuota) return;
            const error = dialog.querySelector<HTMLElement>('#balance-query-error')!;
            error.textContent = '';
            const provider = providerGroup.querySelector<HTMLInputElement>('input:checked:not(:disabled)');
            if (reuseKey.checked && !provider) { error.textContent = '请选择已启用且已配置 API Key 的 Provider'; return; }
            if (!reuseKey.checked && key.required && !key.value) {
              error.textContent = retypeKey ? '修改请求模板后请重新输入 Key，不会复用原有密钥' : '请填写 API Key';
              return;
            }
            setSaving(true);
            try {
              await api.saveBalanceQueryConfig(curl.value, reuseKey.checked ? '' : key.value, reuseKey.checked ? provider!.value : "", false, quotaValue(), isNewApi(), quotaExtras().usd, quotaExtras().type, quotaExtras().symbol, quotaExtras().customRate);
              if (isUnmounted || !dialog.isConnected) return;
              toolRevision++;
              key.value = '';
              const hadToolsDialog = !!closeToolsDialog;
              close();
              toast.success('额度查询配置已保存');
              await returnToToolsDialog(hadToolsDialog);
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
