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
  'SECURITY_CONTROL_MODE',
]);
const booleanKeys = new Set([
  'ENABLE_AT_IN_GROUP_MSG', 'GROUP_REPLY_ON_MENTION', 'ENABLE_REPLY_IN_GROUP_MSG',
  'ENABLE_ONEBOT_ADAPTER', 'ENABLE_ASTRBOT_ADAPTER', 'BILLING_ENABLED', 'ENABLE_PROACTIVE_REPLY',
  'ENABLE_PROACTIVE_REPLY_WHITELIST',
  'SANDBOX_ENABLED', 'ALLOW_REMOTE_MCP_MANAGEMENT', 'MCP_ENFORCE_LOCAL_TOKEN',
]);
import { createInstanceAPI } from '../api/client';
import { instanceState } from '../instance-state';
import { EnvVar } from '@frostagent/proto';
import { escapeHtml, maskSecret } from '../utils/formatters';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { confirmDialog } from '../components/confirm';
import { ProactiveSettingsSync } from './proactive-settings-sync';
import {
  groupIdFromSessionId,
  parseWhitelistGroups,
  formatGroupOption,
  isWhitelistEnabled,
  parseExplicitWhitelistSwitch,
  ProactiveWhitelistSync,
} from './proactive-whitelist';

function configurationBadges(key: string): string {
  const ownership = globalKeys.has(key)
    ? '<small class="badge badge-outline">全局共享</small>'
    : '<small class="badge badge-outline">当前实例</small>';
  return ownership;
}

export function mountBackendSettingsPage(container: HTMLElement): () => void {
  const api = createInstanceAPI();
  let isUnmounted = false;
  let loading = false;
  let envVars: EnvVar[] = [];
  const visibleSecrets = new Set<string>();
  let editingKey: string | null = null;
  let editingValue = '';
  let editingIsSecret = false;

  let groupReplyOnMention = false;
  let enableAtOther = false;
  let enableReplyOther = false;
  let proactiveReplyEnabled = false;
  let proactiveReplyProbability = 0.05;
  let proactiveWhitelistEnabled = false;
  let proactiveWhitelistGroups: string[] = [];
  const sessionGroupMap = new Map<string, string>();

  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="flex items-center justify-between gap-4 flex-wrap pb-1">
        <div class="flex items-center gap-2.5">
          <a href="#/settings" class="btn btn-ghost btn-icon-sm" title="返回设置" style="text-decoration: none;">
            ${icon('arrow_left', 'w-4 h-4')}
          </a>
          <div>
            <h1 class="page-title">Bot 服务端设置</h1>
            <p class="page-description">设置保存在数据库中，修改后自动应用。</p>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <a class="btn btn-outline btn-sm" href="/api/instances/${instanceState.selected!.id}/backup/settings" download="setting.json">下载设置备份</a>
          <button class="btn btn-outline btn-icon-sm" id="backend-refresh-btn" title="刷新">
            ${icon('refresh', 'w-3.5 h-3.5')}
          </button>
        </div>
      </header>

      <div class="flex flex-col gap-4">
        <!-- Group Behavior Settings Card -->
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

              <!-- Whitelist Mode Section -->
              <div class="pt-3 mt-1 border-t border-border/60 flex flex-col gap-2.5">
                <div class="flex items-center justify-between flex-wrap gap-2">
                  <div class="flex items-center gap-1.5">
                    <span class="text-xs font-semibold text-foreground">群聊白名单模式</span>
                    <span class="text-[11px] text-muted font-mono">(ENABLE_PROACTIVE_REPLY_WHITELIST)</span>
                  </div>
                  <label class="flex items-center gap-2 cursor-pointer">
                    <span class="text-xs font-medium text-muted" id="proactive-whitelist-status-text">已停用</span>
                    <input type="checkbox" id="proactive-whitelist-cb" class="checkbox" />
                  </label>
                </div>
                <p class="text-[11px] text-muted">
                  启用白名单模式后，仅白名单内的群聊才会触发主动回复；非白名单中的群永远不触发自动回复。
                </p>

                <div class="flex flex-col gap-2 pt-1" id="proactive-whitelist-controls">
                  <!-- Add Group Input & Select Bar -->
                  <div class="flex items-center gap-2 flex-wrap sm:flex-nowrap">
                    <select id="proactive-whitelist-select" class="input input-sm text-xs flex-1 min-w-[200px]">
                      <option value="">-- 从最近会话选择群聊 --</option>
                    </select>
                    <div class="flex items-center gap-2 w-full sm:w-auto">
                      <input
                        type="text"
                        id="proactive-whitelist-input"
                        class="input input-sm text-xs font-mono flex-1 sm:w-44"
                        placeholder="或手动输入群号"
                      />
                      <button type="button" id="proactive-whitelist-add-btn" class="btn btn-secondary btn-sm text-xs shrink-0 flex items-center gap-1">
                        ${icon('plus', 'w-3 h-3')}
                        <span>添加群</span>
                      </button>
                    </div>
                  </div>

                  <!-- Tag List of Whitelisted Groups -->
                  <div class="flex flex-col gap-1.5">
                    <span class="text-[11px] font-medium text-muted">已配置白名单群：</span>
                    <div id="proactive-whitelist-tags" class="flex flex-wrap gap-1.5 min-h-[1.75rem] items-center p-2 rounded bg-background border border-border">
                      <span class="text-[11px] text-muted">暂无白名单群聊</span>
                    </div>
                  </div>
                </div>
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

        <!-- Settings table -->
        <div class="card table-card overflow-hidden">
          <div class="table-container">
            <table class="table env-table">
              <thead>
                <tr>
                  <th class="env-table-key-col">设置项</th>
                  <th class="env-table-val-col">值</th>
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

    </div>
  `;

  // Elements
  const refreshBtn = container.querySelector<HTMLButtonElement>(
    '#backend-refresh-btn',
  )!;

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

  const proactiveWhitelistCb =
    container.querySelector<HTMLInputElement>('#proactive-whitelist-cb')!;
  const proactiveWhitelistStatusText = container.querySelector<HTMLElement>(
    '#proactive-whitelist-status-text',
  )!;
  const proactiveWhitelistSelect = container.querySelector<HTMLSelectElement>(
    '#proactive-whitelist-select',
  )!;
  const proactiveWhitelistInput = container.querySelector<HTMLInputElement>(
    '#proactive-whitelist-input',
  )!;
  const proactiveWhitelistAddBtn = container.querySelector<HTMLButtonElement>(
    '#proactive-whitelist-add-btn',
  )!;
  const proactiveWhitelistTags = container.querySelector<HTMLElement>(
    '#proactive-whitelist-tags',
  )!;

  const groupMentionCb =
    container.querySelector<HTMLInputElement>('#group-mention-cb')!;
  const groupAtCb = container.querySelector<HTMLInputElement>('#group-at-cb')!;
  const groupReplyCb =
    container.querySelector<HTMLInputElement>('#group-reply-cb')!;

  const tbody = container.querySelector<HTMLElement>('#env-table-body')!;

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
    updateWhitelistUI(isSaving);
  }

  function updateWhitelistUI(isSaving = false) {
    const state = whitelistSync.getState();
    const enabled = state.enabled;
    const groups = state.groups;
    const isUnverified = state.isUnverified;

    proactiveWhitelistCb.checked = enabled;
    proactiveWhitelistCb.disabled = isSaving;
    proactiveWhitelistSelect.disabled = isSaving;
    proactiveWhitelistInput.disabled = isSaving;
    proactiveWhitelistAddBtn.disabled = isSaving;

    proactiveWhitelistStatusText.textContent = isSaving
      ? '保存中...'
      : isUnverified
        ? '状态未确认 (已恢复)'
        : enabled
          ? '已启用'
          : '已停用';
    proactiveWhitelistStatusText.className = isSaving
      ? 'text-xs font-medium text-muted'
      : isUnverified
        ? 'text-xs font-medium text-warning'
        : enabled
          ? 'text-xs font-medium text-primary'
          : 'text-xs font-medium text-muted';

    // Update select options
    const currentSelected = proactiveWhitelistSelect.value;
    const optionsHtml = ['<option value="">-- 从最近会话选择群聊 --</option>'];
    for (const [gid, gname] of sessionGroupMap.entries()) {
      const label = formatGroupOption(gid, gname);
      optionsHtml.push(
        `<option value="${escapeHtml(gid)}">${escapeHtml(label)}</option>`,
      );
    }
    proactiveWhitelistSelect.innerHTML = optionsHtml.join('');
    if (sessionGroupMap.has(currentSelected)) {
      proactiveWhitelistSelect.value = currentSelected;
    }

    // Render tags
    if (groups.length === 0) {
      proactiveWhitelistTags.innerHTML =
        '<span class="text-[11px] text-muted">白名单为空（开启状态下所有群聊均不会触发主动回复）</span>';
    } else {
      proactiveWhitelistTags.innerHTML = groups
        .map((gid) => {
          const cachedName = sessionGroupMap.get(gid);
          const label = formatGroupOption(gid, cachedName);
          return `
            <span class="inline-flex items-center gap-1.5 px-2 py-0.5 rounded bg-muted border border-border text-xs text-foreground font-mono">
              <span>${escapeHtml(label)}</span>
              <button
                type="button"
                class="text-muted hover:text-danger ml-0.5 transition-colors cursor-pointer proactive-remove-group-btn"
                data-group-id="${escapeHtml(gid)}"
                title="移除群号"
                ${isSaving ? 'disabled' : ''}
              >&times;</button>
            </span>
          `;
        })
        .join('');

      proactiveWhitelistTags
        .querySelectorAll<HTMLButtonElement>('.proactive-remove-group-btn')
        .forEach((btn) => {
          btn.disabled = isSaving;
          btn.addEventListener('click', () => {
            if (isSaving) return;
            const gid = btn.dataset.groupId;
            if (gid) {
              void whitelistSync.removeGroup(gid);
            }
          });
        });
    }
  }

  const whitelistSync = new ProactiveWhitelistSync(
    api,
    { enabled: proactiveWhitelistEnabled, groups: proactiveWhitelistGroups },
    {
      onStateChange: (state) => {
        proactiveWhitelistEnabled = state.enabled;
        proactiveWhitelistGroups = state.groups;
        updateWhitelistUI(state.isSaving);
      },
      onError: (err) => {
        toast.error('白名单更新失败: ' + err.message);
      },
      onSuccess: (msg) => {
        toast.success(msg);
      },
      onReloadNeeded: async () => {
        const ok = await loadData();
        if (!ok) {
          throw new Error('重新加载配置失败');
        }
      },
    },
  );

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
    const whitelistLoadSeq = whitelistSync.nextLoadSeq();
    loading = true;
    renderTable();

    try {
      const [vars, sessionsResp] = await Promise.all([
        api.listEnvVars(),
        api.getSessions(100).catch(() => ({ sessions: [] })),
      ]);
      if (isUnmounted) return;
      envVars = vars;

      sessionGroupMap.clear();
      for (const sess of sessionsResp.sessions || []) {
        const gid = groupIdFromSessionId(sess.sessionId);
        if (gid) {
          if (sess.groupName) {
            sessionGroupMap.set(gid, sess.groupName);
          } else if (!sessionGroupMap.has(gid)) {
            sessionGroupMap.set(gid, '');
          }
        }
      }

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

      const whitelistEnStr = getVal('ENABLE_PROACTIVE_REPLY_WHITELIST');
      const whitelistGroupsStr = getVal('PROACTIVE_REPLY_GROUP_WHITELIST');
      const parsedWhitelistGroups = parseWhitelistGroups(whitelistGroupsStr);
      const serverWhitelistEnabled = isWhitelistEnabled(
        whitelistEnStr,
        parsedWhitelistGroups.length,
      );
      const serverExplicitSwitch = parseExplicitWhitelistSwitch(whitelistEnStr);

      groupMentionCb.checked = groupReplyOnMention;
      groupAtCb.checked = enableAtOther;
      groupReplyCb.checked = enableReplyOther;
      proactiveSync.applyServerConfig(serverEnabled, serverProb, loadSeq);
      whitelistSync.applyServerConfig(
        serverWhitelistEnabled,
        parsedWhitelistGroups,
        whitelistLoadSeq,
        serverExplicitSwitch,
      );
      return true;
    } catch (err) {
      if (isUnmounted) return false;
      toast.error(
        '加载设置失败: ' +
          (err instanceof Error ? err.message : String(err)),
      );
      envVars = [];
      return false;
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
            暂无设置项。
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
              ${booleanKeys.has(item.key) ? `<label class="flex items-center gap-2 text-xs"><input type="checkbox" class="checkbox" data-action="toggle-setting" data-key="${escapeHtml(item.key)}" ${item.value === 'true' ? 'checked' : ''} /><span>${item.value === 'true' ? '开启' : '关闭'}</span></label>` : `
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
              `}
            </td>
            <td class="align-top" style="text-align: right;">
              ${booleanKeys.has(item.key) ? '' : `
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
              `}
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
      .querySelectorAll<HTMLInputElement>('[data-action="toggle-setting"]')
      .forEach((input) => {
        input.addEventListener('change', () => {
          if (input.dataset.key) void toggleGroupSetting(input.dataset.key, input.checked);
        });
      });

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
            title: '清除设置项',
            message: `确认清除设置项 ${key} 吗？`,
            confirmLabel: '删除',
            destructive: true,
          });
          if (confirmed) {
            try {
              const res = await api.deleteEnvVar(key);
              if (res.success) {
                toast.success('设置项已清除');
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
        toast.success('设置已保存');
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

  proactiveReplyCb.addEventListener('change', () => {
    void handleProactiveToggle(proactiveReplyCb.checked);
  });
  proactiveReplySlider.addEventListener('input', () => {
    const val = parseFloat(proactiveReplySlider.value);
    if (!isNaN(val)) {
      const clamped = Math.min(1, Math.max(0.01, Math.round(val * 100) / 100));
      proactiveReplyNumber.value = clamped.toFixed(2);
      proactiveReplyProbDisplay.textContent = `${clamped.toFixed(2)} (${Math.round(clamped * 100)}%)`;
    }
  });
  proactiveReplySlider.addEventListener('change', () => {
    void handleProactiveProbChange(parseFloat(proactiveReplySlider.value));
  });
  proactiveReplyNumber.addEventListener('change', () => {
    void handleProactiveProbChange(parseFloat(proactiveReplyNumber.value));
  });
  proactiveReplyNumber.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      proactiveReplyNumber.blur();
    }
  });
  // Whitelist mode event handlers
  proactiveWhitelistCb.addEventListener('change', () => {
    void whitelistSync.toggleEnabled(proactiveWhitelistCb.checked);
  });

  proactiveWhitelistSelect.addEventListener('change', () => {
    if (proactiveWhitelistSelect.value) {
      proactiveWhitelistInput.value = proactiveWhitelistSelect.value;
    }
  });

  proactiveWhitelistAddBtn.addEventListener('click', () => {
    const val =
      proactiveWhitelistInput.value.trim() || proactiveWhitelistSelect.value;
    if (val) {
      proactiveWhitelistInput.value = '';
      proactiveWhitelistSelect.value = '';
      void whitelistSync.addGroup(val);
    }
  });

  proactiveWhitelistInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      const val =
        proactiveWhitelistInput.value.trim() || proactiveWhitelistSelect.value;
      if (val) {
        proactiveWhitelistInput.value = '';
        proactiveWhitelistSelect.value = '';
        void whitelistSync.addGroup(val);
      }
    }
  });

  refreshBtn.addEventListener('click', () => void loadData());

  void loadData();

  return () => {
    isUnmounted = true;
  };
}
