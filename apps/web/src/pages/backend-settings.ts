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
import { createInstanceAPI, extraEnvAPI, type ExtraEnvEntry } from '../api/client';
import { instanceState } from '../instance-state';
import { EnvVar } from '@frostagent/proto';
import { escapeHtml } from '../utils/formatters';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { confirmDialog } from '../components/confirm';
import { ProactiveSettingsSync } from './proactive-settings-sync';
import { renderBackendSettingsForm } from './backend-settings-form';
import {
  groupIdFromSessionId,
  parseWhitelistGroups,
  formatGroupOption,
  isWhitelistEnabled,
  parseExplicitWhitelistSwitch,
  ProactiveWhitelistSync,
} from './proactive-whitelist';

export function mountBackendSettingsPage(container: HTMLElement): () => void {
  const api = createInstanceAPI();
  let isUnmounted = false;
  let loading = false;
  let envVars: EnvVar[] = [];
  let extraEnvEntries: ExtraEnvEntry[] = [];

  let groupReplyOnMention = false;
  let enableAtOther = false;
  let enableReplyOther = false;
  let proactiveReplyEnabled = false;
  let proactiveReplyProbability = 0.05;
  let stickerActivity = 0;
  let stickerActivityMode: 'bot_turns' | 'all_messages' = 'bot_turns';
  let stickerSaving = false;
  let stickerEditSeq = 0;
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
                </div>
              </div>
              <div class="flex items-center gap-2">
                <span class="text-xs font-medium text-muted" id="proactive-reply-status-text" aria-live="polite" style="display: none;"></span>
                <label class="toggle-switch" title="主动回复">
                  <input type="checkbox" id="proactive-reply-cb" role="switch" aria-label="主动回复" />
                  <span aria-hidden="true"></span>
                </label>
              </div>
            </div>

            <div class="p-3 bg-muted/40 rounded-lg border border-border flex flex-col gap-2.5" id="proactive-reply-controls" style="transition: opacity 0.2s ease;">
              <div class="flex items-center justify-between">
                <div class="flex items-center gap-1.5">
                  <span class="text-xs font-semibold text-foreground">触发概率</span>
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
                <span>1.00 (100%)</span>
              </div>

              <!-- Whitelist Mode Section -->
              <div class="pt-3 mt-1 border-t border-border/60 flex flex-col gap-2.5">
                <div class="flex items-center justify-between flex-wrap gap-2">
                  <div class="flex items-center gap-1.5">
                    <span class="text-xs font-semibold text-foreground">群聊白名单模式</span>
                  </div>
                  <div class="flex items-center gap-2">
                    <span class="text-xs font-medium text-muted" id="proactive-whitelist-status-text" aria-live="polite" style="display: none;"></span>
                    <label class="toggle-switch" title="群聊白名单模式">
                      <input type="checkbox" id="proactive-whitelist-cb" role="switch" aria-label="群聊白名单模式" />
                      <span aria-hidden="true"></span>
                    </label>
                  </div>
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

          <div class="mb-4 pb-4 border-b border-border" id="sticker-activity-card">
            <div class="flex items-center justify-between mb-3 gap-2">
              <h3 class="text-xs font-semibold text-foreground">表情包使用积极度</h3>
              <div class="flex items-center gap-2">
                <span class="text-xs font-mono font-semibold text-primary" id="sticker-activity-display" aria-live="polite">0 · 已禁用</span>
                <input type="number" id="sticker-activity-number" class="input input-sm w-16 text-right" min="0" max="10" step="1" value="0" required aria-label="表情包使用积极度数值" />
              </div>
            </div>
            <label for="sticker-activity-mode" class="text-xs font-medium text-foreground">计数方式</label>
            <select id="sticker-activity-mode" class="select w-full mt-1 mb-2">
              <option value="bot_turns">强制 Bot 对话轮次</option>
              <option value="all_messages">全部轮次</option>
            </select>
            <p id="sticker-activity-mode-description" class="text-[11px] text-muted mb-3">每次唤醒或触发主动回复计一轮。</p>
            <input type="range" id="sticker-activity-slider" class="slider w-full" min="0" max="10" step="1" value="0" aria-label="表情包使用积极度" />
            <div class="flex justify-between text-[11px] text-muted mt-1">
              <span>0 · 禁用</span><span>10 · 每 5 轮</span>
            </div>
          </div>

          <!-- Group Response Rules -->
          <div>
            <h3 class="text-xs font-semibold text-foreground mb-2.5">群聊回复规则</h3>
            <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
              <label class="card p-3.5 flex items-center gap-2.5 cursor-pointer hover-bg transition-colors">
                <span class="toggle-switch"><input type="checkbox" id="group-mention-cb" role="switch" aria-label="被 @ 时触发回复" /><span aria-hidden="true"></span></span>
                <div>
                  <span class="text-xs font-semibold text-foreground">被 @ 时触发回复</span>
                </div>
              </label>

              <label class="card p-3.5 flex items-center gap-2.5 cursor-pointer hover-bg transition-colors">
                <span class="toggle-switch"><input type="checkbox" id="group-at-cb" role="switch" aria-label="回复时 @ 对方" /><span aria-hidden="true"></span></span>
                <div>
                  <span class="text-xs font-semibold text-foreground">回复时 @ 对方</span>
                </div>
              </label>

              <label class="card p-3.5 flex items-center gap-2.5 cursor-pointer hover-bg transition-colors">
                <span class="toggle-switch"><input type="checkbox" id="group-reply-cb" role="switch" aria-label="引用对方消息" /><span aria-hidden="true"></span></span>
                <div>
                  <span class="text-xs font-semibold text-foreground">引用/回复对方消息</span>
                </div>
              </label>
            </div>
          </div>
        </article>

        <div id="settings-form" class="flex flex-col gap-4"></div>
        <article class="card p-4">
          <h2 class="card-title text-sm font-semibold">额外 env 注入</h2>
          <p class="text-xs text-muted mt-1 mb-4">供外部组件读取的环境变量。保存或删除后需重启 FrostAgent 才会生效；宿主环境中已有的同名变量优先。</p>
          <div id="extra-env-list" class="flex flex-col gap-2 mb-4"></div>
          <div class="flex flex-col sm:flex-row gap-2">
            <input id="extra-env-key" class="input input-sm sm:w-48" aria-label="额外环境变量名" placeholder="变量名" autocomplete="off" />
            <input id="extra-env-value" class="input input-sm flex-1" type="password" aria-label="额外环境变量值" placeholder="变量值" autocomplete="off" />
            <button id="extra-env-save" type="button" class="btn btn-primary btn-sm">添加或更新</button>
          </div>
        </article>
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
  const stickerActivityModeSelect = container.querySelector<HTMLSelectElement>(
    '#sticker-activity-mode',
  )!;
  const stickerActivityModeDescription = container.querySelector<HTMLElement>(
    '#sticker-activity-mode-description',
  )!;
  const stickerActivitySlider = container.querySelector<HTMLInputElement>(
    '#sticker-activity-slider',
  )!;
  const stickerActivityNumber = container.querySelector<HTMLInputElement>(
    '#sticker-activity-number',
  )!;
  const stickerActivityDisplay = container.querySelector<HTMLElement>(
    '#sticker-activity-display',
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

  const settingsRoot = container.querySelector<HTMLElement>('#settings-form')!;
  const extraEnvList = container.querySelector<HTMLElement>('#extra-env-list')!;
  const extraEnvKey = container.querySelector<HTMLInputElement>('#extra-env-key')!;
  const extraEnvValue = container.querySelector<HTMLInputElement>('#extra-env-value')!;
  const extraEnvSave = container.querySelector<HTMLButtonElement>('#extra-env-save')!;

  function updateProactiveUI(
    enabled: boolean,
    prob: number,
    isSaving = false,
  ) {
    proactiveReplyCb.checked = enabled;
    proactiveReplyCb.disabled = isSaving;
    proactiveReplyStatusText.textContent = isSaving ? '保存中...' : '';
    proactiveReplyStatusText.style.display = isSaving ? 'inline' : 'none';
    proactiveReplyStatusText.className = 'text-xs font-medium text-muted';

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

  function updateStickerActivityUI(level: number) {
    stickerActivitySlider.value = String(level);
    stickerActivitySlider.disabled = stickerSaving;
    stickerActivityNumber.value = String(level);
    stickerActivityNumber.disabled = stickerSaving;
    stickerActivityModeSelect.value = stickerActivityMode;
    stickerActivityModeSelect.disabled = stickerSaving;
    stickerActivityModeDescription.textContent =
      stickerActivityMode === 'all_messages'
        ? '每条传入消息计一轮，无需唤醒 Bot。'
        : '每次唤醒或触发主动回复计一轮。';
    stickerActivityDisplay.textContent =
      level === 0 ? '0 · 已禁用' : `${level} · 每 ${(11 - level) * 5} 轮`;
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
        ? '状态未确认（已恢复）'
        : '';
    proactiveWhitelistStatusText.style.display =
      isSaving || isUnverified ? 'inline' : 'none';
    proactiveWhitelistStatusText.className = isSaving
      ? 'text-xs font-medium text-muted'
      : isUnverified
        ? 'text-xs font-medium text-warning'
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
    const stickerLoadSeq = stickerEditSeq;
    const whitelistLoadSeq = whitelistSync.nextLoadSeq();
    loading = true;
    renderTable();

    try {
      const [vars, sessionsResp] = await Promise.all([
        api.listEnvVars(),
        api.getSessions(100).catch(() => ({ sessions: [] })),
      ]);
      if (isUnmounted) return;
      envVars = vars.filter((item) => item.key !== 'SYSTEM_PROMPT');
      void loadExtraEnv();

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
      if (!stickerSaving && stickerLoadSeq === stickerEditSeq) {
        const parsedActivity = Number(getVal('STICKER_ACTIVITY'));
        stickerActivity =
          Number.isInteger(parsedActivity) &&
          parsedActivity >= 0 &&
          parsedActivity <= 10
            ? parsedActivity
            : 0;
        stickerActivityMode =
          getVal('STICKER_ACTIVITY_MODE') === 'all_messages'
            ? 'all_messages'
            : 'bot_turns';
        updateStickerActivityUI(stickerActivity);
      }
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
      settingsRoot.innerHTML = '<div class="card p-8 text-center text-muted"><span class="spinner"></span> 加载中...</div>';
      return;
    }
    renderBackendSettingsForm(
      settingsRoot,
      envVars,
      (key) => globalKeys.has(key),
      saveEnvVar,
      clearSetting,
    );
  }

  async function clearSetting(key: string, label: string): Promise<void> {
    const confirmed = await confirmDialog({
      title: '恢复默认值',
      message: '确认清除“' + label + '”的自定义值吗？',
      confirmLabel: '清除',
      destructive: true,
    });
    if (!confirmed) return;
    try {
      const result = await api.deleteEnvVar(key);
      if (!result.success) throw new Error(result.error);
      toast.success('已恢复默认值');
      await loadData();
    } catch (error) {
      toast.error('清除失败: ' + (error instanceof Error ? error.message : String(error)));
    }
  }

  function renderExtraEnv(): void {
    if (extraEnvEntries.length === 0) {
      extraEnvList.innerHTML = '<p class="text-xs text-muted">暂无额外注入项。</p>';
      return;
    }
    extraEnvList.innerHTML = extraEnvEntries.map(({ key, value }) => `
      <div class="flex items-center gap-2 min-w-0" data-extra-key="${escapeHtml(key)}">
        <span class="font-mono text-xs w-40 shrink-0 break-all">${escapeHtml(key)}</span>
        <input class="input input-sm flex-1 min-w-0" type="password" readonly value="${escapeHtml(value)}" aria-label="${escapeHtml(key)}的值" />
        <button type="button" class="btn btn-outline btn-sm" data-extra-show>显示</button>
        <button type="button" class="btn btn-outline btn-sm" data-extra-edit>编辑</button>
        <button type="button" class="btn btn-ghost btn-sm text-destructive" data-extra-delete>删除</button>
      </div>`).join('');
    extraEnvList.querySelectorAll<HTMLElement>('[data-extra-key]').forEach((row) => {
      const key = row.dataset.extraKey!;
      const input = row.querySelector<HTMLInputElement>('input')!;
      row.querySelector<HTMLButtonElement>('[data-extra-show]')?.addEventListener('click', (event) => {
        input.type = input.type === 'password' ? 'text' : 'password';
        (event.currentTarget as HTMLButtonElement).textContent = input.type === 'password' ? '显示' : '隐藏';
      });
      row.querySelector<HTMLButtonElement>('[data-extra-edit]')?.addEventListener('click', () => {
        extraEnvKey.value = key;
        extraEnvValue.value = input.value;
        extraEnvValue.focus();
      });
      row.querySelector<HTMLButtonElement>('[data-extra-delete]')?.addEventListener('click', async () => {
        const confirmed = await confirmDialog({
          title: '删除额外环境变量',
          message: '确认删除 ' + key + ' 吗？重启 FrostAgent 后生效。',
          confirmLabel: '删除',
          destructive: true,
        });
        if (!confirmed) return;
        try {
          await extraEnvAPI.delete(key);
          toast.success('已删除，重启 FrostAgent 后生效');
          await loadExtraEnv();
        } catch (error) {
          toast.error('删除失败: ' + (error instanceof Error ? error.message : String(error)));
        }
      });
    });
  }

  async function loadExtraEnv(): Promise<void> {
    try {
      const entries = await extraEnvAPI.list();
      if (isUnmounted) return;
      extraEnvEntries = entries;
      renderExtraEnv();
    } catch (error) {
      if (!isUnmounted) toast.error('加载额外环境变量失败: ' + (error instanceof Error ? error.message : String(error)));
    }
  }

  extraEnvSave.addEventListener('click', async () => {
    const key = extraEnvKey.value.trim();
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) {
      toast.error('请输入有效的环境变量名');
      return;
    }
    extraEnvSave.disabled = true;
    try {
      await extraEnvAPI.put({ key, value: extraEnvValue.value });
      extraEnvKey.value = '';
      extraEnvValue.value = '';
      toast.success('已保存，重启 FrostAgent 后生效');
      await loadExtraEnv();
    } catch (error) {
      toast.error('保存失败: ' + (error instanceof Error ? error.message : String(error)));
    } finally {
      extraEnvSave.disabled = false;
    }
  });

  async function saveEnvVar(key: string, value: string, isSecret: boolean): Promise<boolean> {
    if (!key) return false;
    try {
      const res = await api.updateEnvVar({ key, value, isSecret });
      if (res.success) {
        toast.success('设置已保存');
        void loadData();
        return true;
      } else {
        toast.error('保存失败: ' + res.error);
        return false;
      }
    } catch (err) {
      toast.error(
        '保存失败: ' + (err instanceof Error ? err.message : String(err)),
      );
      return false;
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

  // Proactive reply event handlers
  stickerActivitySlider.addEventListener('input', () => {
    updateStickerActivityUI(Number(stickerActivitySlider.value));
  });

  stickerActivitySlider.addEventListener('change', () => {
    void saveStickerSetting('STICKER_ACTIVITY', stickerActivitySlider.value);
  });

  stickerActivityModeSelect.addEventListener('change', () => {
    void saveStickerSetting(
      'STICKER_ACTIVITY_MODE',
      stickerActivityModeSelect.value,
    );
  });

  async function saveStickerSetting(
    key: 'STICKER_ACTIVITY' | 'STICKER_ACTIVITY_MODE',
    value: string,
  ) {
    if (stickerSaving) return;
    stickerEditSeq++;
    stickerSaving = true;
    updateStickerActivityUI(
      key === 'STICKER_ACTIVITY' ? Number(value) : stickerActivity,
    );
    let reload = false;
    try {
      const res = await api.updateEnvVar({ key, value, isSecret: false });
      if (!res.success)
        throw new Error(res.error || '更新表情包使用积极度失败');
      if (key === 'STICKER_ACTIVITY') stickerActivity = Number(value);
      else
        stickerActivityMode =
          value === 'all_messages' ? 'all_messages' : 'bot_turns';
      if (!isUnmounted) toast.success('表情包使用积极度已更新');
    } catch (err) {
      if (!isUnmounted)
        toast.error(
          '更新失败: ' + (err instanceof Error ? err.message : String(err)),
        );
      reload = true;
    } finally {
      stickerSaving = false;
      if (!isUnmounted) updateStickerActivityUI(stickerActivity);
    }
    if (reload && !isUnmounted) await loadData();
  }
  stickerActivityNumber.addEventListener('change', () => {
    const level = Number(stickerActivityNumber.value);
    if (!stickerActivityNumber.value || !stickerActivityNumber.reportValidity() || !Number.isInteger(level)) {
      updateStickerActivityUI(stickerActivity);
      return;
    }
    void saveStickerSetting('STICKER_ACTIVITY', String(level));
  });

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
