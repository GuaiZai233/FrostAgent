const globalKeys = new Set([
  'LISTEN_ADDR',
  'WS_LISTEN_ADDR',
  'WS_ALLOWED_ORIGINS',
  'HTTP_ALLOWED_ORIGINS',
  'ALCYONE_BASE_URL',
  'ALCYONE_SERVICE_TOKEN',
  'ALCYONE_TIMEOUT',
  'SANDBOX_ENABLED',
  'SANDBOX_BASE_URL',
  'SANDBOX_AUTH_TOKEN',
  'SANDBOX_SESSION_NAMESPACE',
  'MCP_CONTROL_TOKEN',
  'ADMIN_TOKEN',
  'ALLOW_REMOTE_MCP_MANAGEMENT',
  'MCP_ENFORCE_LOCAL_TOKEN',
]);
const instanceRestartKeys = new Set([
  'ENABLE_ONEBOT_ADAPTER',
  'ENABLE_ASTRBOT_ADAPTER',
  'MEMORY_REFLECTION_TIMEOUT',
  'GROUP_COMPACT_BUFFER_SIZE',
  'GROUP_COMPACT_MAX_BUFFER_SIZE',
  'GROUP_COMPACT_MIN_INTERVAL',
  'BILLING_ENABLED',
  'BILLING_MAX_OUTPUT_TOKENS',
  'BILLING_SAFETY_MULTIPLIER',
  'BILLING_PROMPT_PRICE_PER_MILLION',
  'BILLING_COMPLETION_PRICE_PER_MILLION',
  'AGENT_MAX_ITERATIONS',
]);
const controlPlaneRestartKeys = new Set([
  'LISTEN_ADDR',
  'WS_LISTEN_ADDR',
  'HTTP_ALLOWED_ORIGINS',
  'ALCYONE_BASE_URL',
  'ALCYONE_SERVICE_TOKEN',
  'ALCYONE_TIMEOUT',
  'SANDBOX_BASE_URL',
  'SANDBOX_AUTH_TOKEN',
  'SANDBOX_SESSION_NAMESPACE',
  'MCP_CONTROL_TOKEN',
  'ADMIN_TOKEN',
  'ALLOW_REMOTE_MCP_MANAGEMENT',
  'MCP_ENFORCE_LOCAL_TOKEN',
]);
import { createInstanceAPI } from '../api/client';
import { EnvVar } from '@frostagent/proto';
import { escapeHtml, maskSecret } from '../utils/formatters';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { openDialog } from '../components/dialog';
import { confirmDialog } from '../components/confirm';
import { ProactiveSettingsSync } from './proactive-settings-sync';

function configurationBadges(key: string): string {
  const ownership = globalKeys.has(key)
    ? '<small class="badge badge-outline">全局共享</small>'
    : '<small class="badge badge-outline">当前实例</small>';
  const applyScope = controlPlaneRestartKeys.has(key)
    ? '<small class="badge badge-warning">重启 FrostAgent 后生效</small>'
    : instanceRestartKeys.has(key)
      ? '<small class="badge badge-warning">重启实例后生效</small>'
      : '<small class="badge badge-outline">立即生效</small>';
  return ownership + applyScope;
}

export function mountBackendSettingsPage(container: HTMLElement): () => void {
  const api = createInstanceAPI();
  let isUnmounted = false;
  let loading = false;
  let saving = false;
  let envVars: EnvVar[] = [];
  let rawContent = '';
  const visibleSecrets = new Set<string>();
  let editingKey: string | null = null;
  let editingValue = '';
  let editingIsSecret = false;

  let groupReplyOnMention = false;
  let enableAtOther = false;
  let enableReplyOther = false;
  let proactiveReplyEnabled = false;
  let proactiveReplyProbability = 0.05;

  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="flex items-center justify-between gap-4 flex-wrap pb-1">
        <div class="flex items-center gap-2.5">
          <a href="#/settings" class="btn btn-ghost btn-icon-sm" title="返回设置" style="text-decoration: none;">
            ${icon('arrow_left', 'w-4 h-4')}
          </a>
          <div>
            <h1 class="page-title">Bot 服务端设置</h1>
            <p class="page-description">每项配置分别标明归属范围与生效方式；Control Plane 字段需要重启 FrostAgent 时会明确提示。</p>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <button class="btn btn-primary btn-sm" id="add-env-btn">
            ${icon('plus', 'w-3.5 h-3.5')}
            <span>新增环境变量</span>
          </button>
          <button class="btn btn-outline btn-icon-sm" id="backend-refresh-btn" title="刷新">
            ${icon('refresh', 'w-3.5 h-3.5')}
          </button>
        </div>
      </header>

      <!-- Tabs -->
      <div class="tabs">
        <button class="tab-item active" id="tab-table-btn">
          ${icon('table', 'w-3.5 h-3.5')}
          <span>环境变量表</span>
        </button>
        <button class="tab-item" id="tab-raw-btn">
          ${icon('file_text', 'w-3.5 h-3.5')}
          <span>原始 .env</span>
        </button>
      </div>

      <!-- Table View Container -->
      <div id="tab-table-content" class="flex flex-col gap-4">
        <!-- Bot Behavior Settings Card -->
        <article class="card p-4">
          <div class="card-header border-b border-border pb-3 mb-3">
            <div class="flex items-center gap-2">
              <span class="text-primary flex items-center">${icon('bot', 'w-4 h-4')}</span>
              <h2 class="card-title text-sm font-semibold">Bot 行为与回复策略</h2>
            </div>
          </div>

          <!-- Proactive Reply Section -->
          <div class="mb-4 pb-4 border-b border-border" id="proactive-reply-card">
            <div class="flex items-center justify-between mb-3 flex-wrap gap-2">
              <div class="flex items-center gap-2">
                <span class="text-primary flex items-center">${icon('sparkles', 'w-4 h-4')}</span>
                <div>
                  <h3 class="text-xs font-semibold text-foreground">主动回复</h3>
                  <p class="text-[11px] text-muted mt-0.5">未被唤醒的入站群聊消息先 roll 概率；触发后由模型研判值得插嘴则回复，否则调用 stay_silent 静默</p>
                </div>
              </div>
              <label class="flex items-center gap-2 cursor-pointer">
                <span class="text-xs font-medium text-muted" id="proactive-reply-status-text">已停用</span>
                <input type="checkbox" id="proactive-reply-cb" class="checkbox" />
              </label>
            </div>

            <div class="p-3 bg-muted/40 rounded-lg border border-border flex flex-col gap-2.5" id="proactive-reply-controls" style="transition: opacity 0.2s ease;">
              <div class="flex items-center justify-between">
                <div class="flex items-center gap-1.5">
                  <span class="text-xs font-semibold text-foreground">触发概率</span>
                  <span class="text-[11px] text-muted font-mono">(PROACTIVE_REPLY_PROBABILITY)</span>
                </div>
                <div class="flex items-center gap-2">
                  <span class="text-xs font-mono font-semibold text-primary" id="proactive-reply-prob-display">0.05 (5%)</span>
                </div>
              </div>

              <div class="flex items-center gap-4 pt-1">
                <input
                  type="range"
                  id="proactive-reply-slider"
                  class="slider flex-1"
                  min="0.01"
                  max="1.00"
                  step="0.01"
                  value="0.05"
                />
                <div class="flex items-center gap-1.5" style="width: 7.5rem;">
                  <input
                    type="number"
                    id="proactive-reply-number"
                    class="input input-sm font-mono text-right"
                    min="0.01"
                    max="1.00"
                    step="0.01"
                    value="0.05"
                    placeholder="0.05"
                    style="width: 5.5rem;"
                  />
                  <span class="text-xs text-muted">/ 1.0</span>
                </div>
              </div>

              <div class="flex items-center justify-between text-[11px] text-muted mt-0.5">
                <span>0.01 (1%)</span>
                <span>精度 0.01 ~ 1.00，开启后最低 0.01，支持滑块或手动输入</span>
                <span>1.00 (100%)</span>
              </div>
            </div>
          </div>

          <!-- Group Response Rules -->
          <div>
            <h3 class="text-xs font-semibold text-foreground mb-2.5">群聊回复规则</h3>
            <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
              <label class="card p-3.5 flex items-start gap-2.5 cursor-pointer hover-bg transition-colors">
                <input type="checkbox" id="group-mention-cb" class="checkbox" style="margin-top: 0.125rem;" />
                <div>
                  <span class="text-xs font-semibold text-foreground">被 @ 时触发回复</span>
                  <p class="text-[11px] text-muted font-mono mt-0.5">GROUP_REPLY_ON_MENTION</p>
                </div>
              </label>

              <label class="card p-3.5 flex items-start gap-2.5 cursor-pointer hover-bg transition-colors">
                <input type="checkbox" id="group-at-cb" class="checkbox" style="margin-top: 0.125rem;" />
                <div>
                  <span class="text-xs font-semibold text-foreground">回复时 @ 对方</span>
                  <p class="text-[11px] text-muted font-mono mt-0.5">ENABLE_AT_IN_GROUP_MSG</p>
                </div>
              </label>

              <label class="card p-3.5 flex items-start gap-2.5 cursor-pointer hover-bg transition-colors">
                <input type="checkbox" id="group-reply-cb" class="checkbox" style="margin-top: 0.125rem;" />
                <div>
                  <span class="text-xs font-semibold text-foreground">引用/回复对方消息</span>
                  <p class="text-[11px] text-muted font-mono mt-0.5">ENABLE_REPLY_IN_GROUP_MSG</p>
                </div>
              </label>
            </div>
          </div>
        </article>

        <!-- Env Vars Table Card -->
        <div class="card table-card overflow-hidden">
          <div class="table-container">
            <table class="table env-table">
              <thead>
                <tr>
                  <th class="env-table-key-col">变量名 (Key)</th>
                  <th class="env-table-val-col">变量值 (Value)</th>
                  <th class="env-table-action-col">操作</th>
                </tr>
              </thead>
              <tbody id="env-table-body">
                <tr>
                  <td colspan="3" class="text-center text-muted" style="padding: 2.5rem;">
                    <span class="spinner"></span>
                    <span style="margin-left: 0.5rem;">加载中...</span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>

      <!-- Raw .env View Container -->
      <div id="tab-raw-content" class="flex flex-col gap-3" style="display: none;">
        <article class="card p-4 flex flex-col gap-3.5">
          <div class="flex items-center justify-between">
            <label class="form-label" for="raw-env-textarea">实例 .env 原始文件编辑（不含全局字段）</label>
            <button class="btn btn-primary btn-sm" id="save-raw-env-btn">
              ${icon('save', 'w-3.5 h-3.5')}
              <span>保存 .env 文件</span>
            </button>
          </div>
          <textarea
            id="raw-env-textarea"
            class="textarea font-mono text-xs leading-relaxed"
            rows="20"
            placeholder="KEY=VALUE..."
            style="white-space: pre;"
          ></textarea>
        </article>
      </div>
    </div>
  `;

  // Elements
  const tabTableBtn =
    container.querySelector<HTMLButtonElement>('#tab-table-btn')!;
  const tabRawBtn = container.querySelector<HTMLButtonElement>('#tab-raw-btn')!;
  const tabTableContent =
    container.querySelector<HTMLElement>('#tab-table-content')!;
  const tabRawContent =
    container.querySelector<HTMLElement>('#tab-raw-content')!;

  const refreshBtn = container.querySelector<HTMLButtonElement>(
    '#backend-refresh-btn',
  )!;
  const addEnvBtn = container.querySelector<HTMLButtonElement>('#add-env-btn')!;

  const proactiveReplyCb =
    container.querySelector<HTMLInputElement>('#proactive-reply-cb')!;
  const proactiveReplyStatusText = container.querySelector<HTMLElement>(
    '#proactive-reply-status-text',
  )!;
  const proactiveReplyControls = container.querySelector<HTMLElement>(
    '#proactive-reply-controls',
  )!;
  const proactiveReplySlider = container.querySelector<HTMLInputElement>(
    '#proactive-reply-slider',
  )!;
  const proactiveReplyNumber = container.querySelector<HTMLInputElement>(
    '#proactive-reply-number',
  )!;
  const proactiveReplyProbDisplay = container.querySelector<HTMLElement>(
    '#proactive-reply-prob-display',
  )!;

  const groupMentionCb =
    container.querySelector<HTMLInputElement>('#group-mention-cb')!;
  const groupAtCb = container.querySelector<HTMLInputElement>('#group-at-cb')!;
  const groupReplyCb =
    container.querySelector<HTMLInputElement>('#group-reply-cb')!;

  const tbody = container.querySelector<HTMLElement>('#env-table-body')!;
  const rawTextarea =
    container.querySelector<HTMLTextAreaElement>('#raw-env-textarea')!;
  const saveRawEnvBtn =
    container.querySelector<HTMLButtonElement>('#save-raw-env-btn')!;

  function updateProactiveUI(
    enabled: boolean,
    prob: number,
    isSaving = false,
  ) {
    proactiveReplyCb.checked = enabled;
    proactiveReplyCb.disabled = isSaving;
    proactiveReplyStatusText.textContent = isSaving
      ? '保存中...'
      : enabled
        ? '已启用'
        : '已停用';
    proactiveReplyStatusText.className = enabled
      ? 'text-xs font-medium text-primary'
      : 'text-xs font-medium text-muted';

    const clampedProb = Math.min(
      1.0,
      Math.max(0.01, Math.round(prob * 100) / 100),
    );
    const probStr = clampedProb.toFixed(2);
    proactiveReplySlider.value = probStr;
    proactiveReplyNumber.value = probStr;
    proactiveReplyProbDisplay.textContent = `${probStr} (${Math.round(clampedProb * 100)}%)`;

    proactiveReplySlider.disabled = !enabled || isSaving;
    proactiveReplyNumber.disabled = !enabled || isSaving;
    proactiveReplyControls.style.opacity = !enabled || isSaving ? '0.55' : '1';
    proactiveReplyControls.style.pointerEvents =
      enabled && !isSaving ? 'auto' : 'none';
  }

  const proactiveSync = new ProactiveSettingsSync(
    api,
    { enabled: proactiveReplyEnabled, probability: proactiveReplyProbability },
    {
      onStateChange: (state) => {
        proactiveReplyEnabled = state.enabled;
        proactiveReplyProbability = state.probability;
        updateProactiveUI(state.enabled, state.probability, state.isSaving);
      },
      onError: (err) => {
        toast.error('更新失败: ' + err.message);
      },
      onSuccess: (msg) => {
        toast.success(msg);
      },
      onReloadNeeded: async () => {
        await loadData();
      },
    },
  );

  async function loadData() {
    if (isUnmounted) return;
    const loadSeq = proactiveSync.nextLoadSeq();
    loading = true;
    renderTable();

    try {
      const [vars, raw] = await Promise.all([
        api.listEnvVars(),
        api.getRawEnvFile(),
      ]);
      if (isUnmounted) return;
      envVars = vars;
      rawContent = raw;
      rawTextarea.value = rawContent;

      const getVal = (k: string) => vars.find((v) => v.key === k)?.value ?? '';
      groupReplyOnMention = getVal('GROUP_REPLY_ON_MENTION') !== 'false';
      enableAtOther = getVal('ENABLE_AT_IN_GROUP_MSG') === 'true';
      enableReplyOther = getVal('ENABLE_REPLY_IN_GROUP_MSG') === 'true';

      const probValStr = getVal('PROACTIVE_REPLY_PROBABILITY');
      const enabledValStr = getVal('ENABLE_PROACTIVE_REPLY');
      const parsedProb = parseFloat(probValStr);

      let serverEnabled = false;
      if (enabledValStr === 'false') {
        serverEnabled = false;
      } else if (enabledValStr === 'true') {
        serverEnabled = true;
      } else {
        serverEnabled = !isNaN(parsedProb) && parsedProb > 0;
      }

      let serverProb = 0.05;
      if (!isNaN(parsedProb) && parsedProb >= 0.01 && parsedProb <= 1.0) {
        serverProb = Math.round(parsedProb * 100) / 100;
      } else if (serverEnabled) {
        serverProb = 0.01;
      } else {
        serverProb = 0.05;
      }

      groupMentionCb.checked = groupReplyOnMention;
      groupAtCb.checked = enableAtOther;
      groupReplyCb.checked = enableReplyOther;
      proactiveSync.applyServerConfig(serverEnabled, serverProb, loadSeq);
    } catch (err) {
      if (isUnmounted) return;
      toast.error(
        '加载环境变量失败: ' +
          (err instanceof Error ? err.message : String(err)),
      );
      envVars = [];
    } finally {
      if (!isUnmounted) {
        loading = false;
        renderTable();
      }
    }
  }

  function renderTable() {
    if (loading && envVars.length === 0) {
      tbody.innerHTML = `
        <tr>
          <td colspan="3" class="text-center text-muted" style="padding: 2.5rem;">
            <span class="spinner"></span>
            <span style="margin-left: 0.5rem;">加载中...</span>
          </td>
        </tr>
      `;
      return;
    }

    if (envVars.length === 0) {
      tbody.innerHTML = `
        <tr>
          <td colspan="3" class="text-center text-muted" style="padding: 3rem;">
            暂无环境变量配置。
          </td>
        </tr>
      `;
      return;
    }

    tbody.innerHTML = envVars
      .map((item) => {
        const isEditing = editingKey === item.key;
        const isSecret = item.isSecret;
        const isVisible = visibleSecrets.has(item.key);

        if (isEditing) {
          const isMultiline =
            !editingIsSecret &&
            (editingValue.includes('\n') || editingValue.length > 60);

          return `
            <tr class="bg-muted">
              <td class="align-top">
                <div class="flex items-center gap-1.5 flex-wrap">
                  <span class="font-mono text-xs font-semibold text-foreground break-all">${escapeHtml(item.key)}</span>
                  <div class="flex items-center gap-1 flex-wrap">
                    ${configurationBadges(item.key)}
                  </div>
                </div>
              </td>
              <td class="align-top">
                <div class="flex items-start gap-2 min-w-0">
                  ${
                    editingIsSecret
                      ? `
                    <input
                      type="password"
                      class="input font-mono text-xs flex-1 min-w-0"
                      id="edit-env-val-input"
                      value="${escapeHtml(editingValue)}"
                      style="height: 1.875rem;"
                    />
                  `
                      : isMultiline
                        ? `
                    <textarea
                      class="textarea font-mono text-xs flex-1 min-w-0 leading-relaxed"
                      id="edit-env-val-input"
                      rows="${Math.min(8, Math.max(3, editingValue.split('\n').length))}"
                      style="resize: vertical; min-height: 2.25rem;"
                    >${escapeHtml(editingValue)}</textarea>
                  `
                        : `
                    <input
                      type="text"
                      class="input font-mono text-xs flex-1 min-w-0"
                      id="edit-env-val-input"
                      value="${escapeHtml(editingValue)}"
                      style="height: 1.875rem;"
                    />
                  `
                  }
                  <label class="flex items-center gap-1.5 cursor-pointer text-xs select-none shrink-0" style="white-space: nowrap; flex-shrink: 0; margin-top: 0.25rem;">
                    <input type="checkbox" id="edit-env-secret-cb" class="checkbox" ${editingIsSecret ? 'checked' : ''} />
                    <span class="text-muted">敏感</span>
                  </label>
                </div>
              </td>
              <td class="align-top" style="text-align: right;">
                <div class="flex items-center justify-end gap-1">
                  <button class="btn btn-primary btn-icon-sm" style="width: 1.75rem; height: 1.75rem;" id="save-inline-btn" title="保存">
                    ${icon('check', 'w-3.5 h-3.5')}
                  </button>
                  <button class="btn btn-outline btn-icon-sm" style="width: 1.75rem; height: 1.75rem;" id="cancel-inline-btn" title="取消">
                    ${icon('close', 'w-3.5 h-3.5')}
                  </button>
                </div>
              </td>
            </tr>
          `;
        }

        const displayVal =
          isSecret && !isVisible ? maskSecret(item.value) : item.value;

        return `
          <tr>
            <td class="align-top">
              <div class="flex items-center gap-1.5 flex-wrap">
                ${isSecret ? `<span class="text-muted flex items-center shrink-0" title="敏感配置">${icon('lock', 'w-3.5 h-3.5')}</span>` : ''}
                <span class="font-mono text-xs font-medium select-text text-foreground break-all">${escapeHtml(item.key)}</span>
                <div class="flex items-center gap-1 flex-wrap">
                  ${configurationBadges(item.key)}
                </div>
              </div>
            </td>
            <td class="align-top">
              <div class="flex items-start gap-1.5 min-w-0">
                <span class="font-mono text-xs break-all whitespace-pre-wrap select-text text-foreground flex-1 min-w-0 leading-relaxed">${escapeHtml(displayVal || '（空）')}</span>
                ${
                  isSecret
                    ? `
                  <button class="btn btn-ghost btn-icon-sm text-muted shrink-0" style="width: 1.5rem; height: 1.5rem; padding: 0; flex-shrink: 0;" data-action="toggle-secret" data-key="${escapeHtml(
                    item.key,
                  )}" title="${isVisible ? '隐藏' : '显示'}">
                    ${icon(isVisible ? 'eye_off' : 'eye', 'w-3 h-3')}
                  </button>
                `
                    : ''
                }
              </div>
            </td>
            <td class="align-top" style="text-align: right;">
              <div class="flex items-center justify-end gap-1">
                <button class="btn btn-ghost btn-icon-sm" style="width: 1.75rem; height: 1.75rem;" data-action="edit-env" data-key="${escapeHtml(
                  item.key,
                )}" title="修改">
                  ${icon('pencil', 'w-3.5 h-3.5')}
                </button>
                <button class="btn btn-ghost btn-icon-sm text-destructive" style="width: 1.75rem; height: 1.75rem;" data-action="delete-env" data-key="${escapeHtml(
                  item.key,
                )}" title="删除">
                  ${icon('trash', 'w-3.5 h-3.5')}
                </button>
              </div>
            </td>
          </tr>
        `;
      })
      .join('');

    // Attach inline edit handlers if active
    if (editingKey) {
      const editInput = tbody.querySelector<HTMLInputElement | HTMLTextAreaElement>(
        '#edit-env-val-input',
      );
      const editSecretCb = tbody.querySelector<HTMLInputElement>(
        '#edit-env-secret-cb',
      );
      const saveInlineBtn =
        tbody.querySelector<HTMLButtonElement>('#save-inline-btn');
      const cancelInlineBtn =
        tbody.querySelector<HTMLButtonElement>('#cancel-inline-btn');

      editInput?.focus();
      if (editInput && typeof editInput.setSelectionRange === 'function') {
        const len = editInput.value.length;
        editInput.setSelectionRange(len, len);
      }
      editInput?.addEventListener('input', () => {
        editingValue = editInput.value;
      });
      editSecretCb?.addEventListener('change', () => {
        editingIsSecret = editSecretCb.checked;
        if (editInput) {
          editingValue = editInput.value;
        }
        renderTable();
      });

      saveInlineBtn?.addEventListener('click', async () => {
        await saveEnvVar(editingKey!, editingValue, editingIsSecret);
        editingKey = null;
        renderTable();
      });

      cancelInlineBtn?.addEventListener('click', () => {
        editingKey = null;
        renderTable();
      });

      editInput?.addEventListener('keydown', ((e: KeyboardEvent) => {
        if (e.key === 'Escape') {
          e.preventDefault();
          cancelInlineBtn?.click();
        } else if (
          (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) ||
          (e.key === 'Enter' && editInput instanceof HTMLInputElement)
        ) {
          e.preventDefault();
          saveInlineBtn?.click();
        }
      }) as EventListener);
    }

    // Attach row button events
    tbody
      .querySelectorAll<HTMLButtonElement>('[data-action="toggle-secret"]')
      .forEach((btn) => {
        btn.addEventListener('click', () => {
          const key = btn.dataset.key;
          if (!key) return;
          if (visibleSecrets.has(key)) visibleSecrets.delete(key);
          else visibleSecrets.add(key);
          renderTable();
        });
      });

    tbody
      .querySelectorAll<HTMLButtonElement>('[data-action="edit-env"]')
      .forEach((btn) => {
        btn.addEventListener('click', () => {
          const key = btn.dataset.key;
          const item = envVars.find((v) => v.key === key);
          if (item) {
            editingKey = item.key;
            editingValue = item.value;
            editingIsSecret = item.isSecret;
            renderTable();
          }
        });
      });

    tbody
      .querySelectorAll<HTMLButtonElement>('[data-action="delete-env"]')
      .forEach((btn) => {
        btn.addEventListener('click', async () => {
          const key = btn.dataset.key;
          if (!key) return;
          const confirmed = await confirmDialog({
            title: '删除环境变量',
            message: `确认删除环境变量 ${key} 吗？`,
            confirmLabel: '删除',
            destructive: true,
          });
          if (confirmed) {
            try {
              const res = await api.deleteEnvVar(key);
              if (res.success) {
                toast.success('环境变量已删除');
                void loadData();
              } else {
                toast.error('删除失败: ' + res.error);
              }
            } catch (err) {
              toast.error(
                '删除失败: ' +
                  (err instanceof Error ? err.message : String(err)),
              );
            }
          }
        });
      });
  }

  async function saveEnvVar(key: string, value: string, isSecret: boolean) {
    if (!key) return;
    try {
      const res = await api.updateEnvVar({ key, value, isSecret });
      if (res.success) {
        toast.success('环境变量已保存');
        void loadData();
      } else {
        toast.error('保存失败: ' + res.error);
      }
    } catch (err) {
      toast.error(
        '保存失败: ' + (err instanceof Error ? err.message : String(err)),
      );
    }
  }

  function openAddEnvModal() {
    openDialog({
      title: '新增环境变量',
      description: '添加或覆盖服务端使用的环境变量。',
      maxWidth: '32rem',
      bodyHtml: `
        <div class="flex flex-col gap-3.5">
          <div class="form-group">
            <label class="form-label" for="add-env-key">Key <span class="text-destructive">*</span></label>
            <input id="add-env-key" class="input font-mono text-xs" placeholder="如 SYSTEM_PROMPT, BOT_NAME..." autocomplete="off" />
          </div>
          <div class="form-group">
            <label class="form-label" for="add-env-val">Value</label>
            <input id="add-env-val" class="input font-mono text-xs" placeholder="环境变量值..." autocomplete="off" />
          </div>
          <label class="flex items-center gap-2 cursor-pointer text-xs select-none">
            <input type="checkbox" id="add-env-secret-cb" class="checkbox" />
            <span class="text-muted">这是敏感信息（自动脱敏显示）</span>
          </label>
        </div>
      `,
      footerHtml: `
        <button class="btn btn-outline btn-sm" id="add-env-cancel">取消</button>
        <button class="btn btn-primary btn-sm" id="add-env-save">
          ${icon('save', 'w-3.5 h-3.5')}
          <span>保存</span>
        </button>
      `,
      onMount: (dialogEl, close) => {
        const keyInput =
          dialogEl.querySelector<HTMLInputElement>('#add-env-key')!;
        const valInput =
          dialogEl.querySelector<HTMLInputElement>('#add-env-val')!;
        const secretCb =
          dialogEl.querySelector<HTMLInputElement>('#add-env-secret-cb')!;
        const saveBtn =
          dialogEl.querySelector<HTMLButtonElement>('#add-env-save')!;
        const cancelBtn =
          dialogEl.querySelector<HTMLButtonElement>('#add-env-cancel')!;

        secretCb.addEventListener('change', () => {
          valInput.type = secretCb.checked ? 'password' : 'text';
        });

        cancelBtn.addEventListener('click', () => close());
        saveBtn.addEventListener('click', async () => {
          const key = keyInput.value.trim();
          if (!key) {
            toast.error('Key 不能为空');
            return;
          }
          const value = valInput.value;
          const isSecret = secretCb.checked;

          try {
            saveBtn.disabled = true;
            const res = await api.updateEnvVar({ key, value, isSecret });
            if (res.success) {
              toast.success('环境变量已保存');
              close();
              void loadData();
            } else {
              toast.error('保存失败: ' + res.error);
            }
          } catch (err) {
            toast.error(
              '保存失败: ' + (err instanceof Error ? err.message : String(err)),
            );
          } finally {
            saveBtn.disabled = false;
          }
        });
      },
    });
  }

  // Toggle behavior settings
  async function toggleGroupSetting(key: string, value: boolean) {
    try {
      const res = await api.updateEnvVar({
        key,
        value: value ? 'true' : 'false',
        isSecret: false,
      });
      if (res.success) {
        toast.success('群聊设置已更新');
        void loadData();
      } else {
        toast.error('更新失败: ' + res.error);
      }
    } catch (err) {
      toast.error(
        '更新失败: ' + (err instanceof Error ? err.message : String(err)),
      );
    }
  }

  // Proactive reply handlers
  async function handleProactiveToggle(enabled: boolean) {
    let prob = parseFloat(proactiveReplyNumber.value);
    if (isNaN(prob) || prob < 0.01) {
      prob = 0.01;
    }
    if (prob > 1.0) {
      prob = 1.0;
    }
    await proactiveSync.setTarget(enabled, prob);
  }

  async function handleProactiveProbChange(val: number) {
    await proactiveSync.setTarget(proactiveReplyEnabled, val);
  }

  // Raw .env save
  async function saveRawEnv() {
    if (saving) return;
    saving = true;
    saveRawEnvBtn.disabled = true;
    try {
      const content = rawTextarea.value;
      const res = await api.updateRawEnvFile(content);
      if (res.success) {
        toast.success('.env 文件已更新并已重载配置');
        void loadData();
      } else {
        toast.error('更新失败: ' + res.error);
      }
    } catch (err) {
      toast.error(
        '更新失败: ' + (err instanceof Error ? err.message : String(err)),
      );
    } finally {
      saving = false;
      saveRawEnvBtn.disabled = false;
    }
  }

  // Tab switching
  tabTableBtn.addEventListener('click', () => {
    tabTableBtn.classList.add('active');
    tabRawBtn.classList.remove('active');
    tabTableContent.style.display = 'flex';
    tabRawContent.style.display = 'none';
  });

  tabRawBtn.addEventListener('click', () => {
    tabTableBtn.classList.remove('active');
    tabRawBtn.classList.add('active');
    tabTableContent.style.display = 'none';
    tabRawContent.style.display = 'flex';
  });

  // Group cb handlers
  groupMentionCb.addEventListener(
    'change',
    () =>
      void toggleGroupSetting('GROUP_REPLY_ON_MENTION', groupMentionCb.checked),
  );
  groupAtCb.addEventListener(
    'change',
    () => void toggleGroupSetting('ENABLE_AT_IN_GROUP_MSG', groupAtCb.checked),
  );
  groupReplyCb.addEventListener(
    'change',
    () =>
      void toggleGroupSetting(
        'ENABLE_REPLY_IN_GROUP_MSG',
        groupReplyCb.checked,
      ),
  );

  // Proactive reply event handlers
  proactiveReplyCb.addEventListener('change', () => {
    void handleProactiveToggle(proactiveReplyCb.checked);
  });

  proactiveReplySlider.addEventListener('input', () => {
    const val = parseFloat(proactiveReplySlider.value);
    if (!isNaN(val)) {
      const clamped = Math.min(
        1.0,
        Math.max(0.01, Math.round(val * 100) / 100),
      );
      proactiveReplyNumber.value = clamped.toFixed(2);
      proactiveReplyProbDisplay.textContent = `${clamped.toFixed(2)} (${Math.round(clamped * 100)}%)`;
    }
  });

  proactiveReplySlider.addEventListener('change', () => {
    const val = parseFloat(proactiveReplySlider.value);
    void handleProactiveProbChange(val);
  });

  proactiveReplyNumber.addEventListener('change', () => {
    const val = parseFloat(proactiveReplyNumber.value);
    void handleProactiveProbChange(val);
  });

  proactiveReplyNumber.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      proactiveReplyNumber.blur();
    }
  });

  addEnvBtn.addEventListener('click', openAddEnvModal);
  saveRawEnvBtn.addEventListener('click', () => void saveRawEnv());
  refreshBtn.addEventListener('click', () => void loadData());

  void loadData();

  return () => {
    isUnmounted = true;
  };
}
