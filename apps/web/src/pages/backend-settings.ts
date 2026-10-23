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
  'MCP_ENFORCE_LOCAL_TOKEN',
  'SECURITY_CONTROL_MODE',
]);
import { createInstanceAPI, extraEnvAPI, setControlToken } from '../api/client';
import { instanceState } from '../instance-state';
import { escapeHtml } from '../utils/formatters';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { renderBackendSettingsForm } from './backend-settings-form';
import { SettingsDraft } from './settings-draft';
import { dedicatedSettingKeys, settingFields } from './backend-setting-fields';
import {
  groupIdFromSessionId,
  parseWhitelistGroups,
  formatGroupOption,
  isWhitelistEnabled,
} from './proactive-whitelist';

export function mountBackendSettingsPage(container: HTMLElement, api = createInstanceAPI(), extraAPI = extraEnvAPI): () => void {
  let isUnmounted = false;
  let loading = false;
  let storageBackend = '';
  let saving = false;
  let loadSeq = 0;
  let draft: SettingsDraft | null = null;
  const controller = new AbortController();
  const sessionGroupMap = new Map<string, string>();

  container.innerHTML = `
    <div class="page-container fade-in">
      <div id="settings-draft-banner" class="settings-draft-banner" role="alert" hidden>
        <span>当前更改尚未保存，离开将丢弃。</span>
        <div class="flex gap-2 shrink-0">
          <button type="button" id="settings-draft-restore" class="btn btn-outline btn-sm">还原</button>
          <button type="button" id="settings-draft-save" class="btn btn-outline btn-sm">保存</button>
        </div>
      </div>
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
        <article class="card p-4">
          <div class="flex items-center justify-between gap-3">
            <h2 class="card-title text-sm font-semibold">记忆设置</h2>
            <small class="badge badge-outline">当前实例</small>
          </div>
          <div class="flex items-center justify-between gap-3 mt-3">
            <label class="text-xs font-semibold" for="embedding-enabled">启用Embedding</label>
            <label class="toggle-switch"><input type="checkbox" id="embedding-enabled" role="switch" aria-label="启用Embedding" disabled><span aria-hidden="true"></span></label>
          </div>
          <p id="embedding-sqlite-warning" class="text-xs text-destructive mt-2" hidden>sqlite不支持向量存储，反思后embedding的结果将不会持久化！</p>
        </article>
        <!-- Group Behavior Settings Card -->
        <article class="card p-4">
          <div class="card-header border-b border-border pb-3 mb-3">
            <div class="flex items-center gap-2">
              <span class="text-primary flex items-center">${icon('bot', 'w-4 h-4')}</span>
              <h2 class="card-title text-sm font-semibold">Bot 行为与回复策略</h2>
            </div>
          </div>

          <div class="mb-4 pb-4 border-b border-border" id="poke-card">
            <div class="flex items-center gap-2.5 mb-3">
              <label class="toggle-switch"><input type="checkbox" id="poke-enabled" role="switch" aria-label="允许发送戳一戳行为" /><span aria-hidden="true"></span></label>
              <label for="poke-enabled" class="text-xs font-semibold text-foreground">允许发送戳一戳行为</label>
            </div>
            <div id="poke-controls" class="flex flex-col gap-2.5">
              <label for="poke-custom-text" class="text-xs font-medium">行为附加自定义文本</label>
              <p id="poke-text-description" class="text-[11px] text-muted">一旦Bot被戳，发送给LLM的附加消息。</p>
              <input type="text" id="poke-custom-text" class="input input-sm settings-default-input w-full min-w-0" placeholder="的..." aria-describedby="poke-text-description poke-preview" />
              <p id="poke-preview" class="text-[11px] text-muted break-all" aria-live="polite"></p>
              <div class="flex items-center gap-2 flex-wrap">
                <label for="poke-cooldown" class="text-xs font-medium">冷却时间</label>
                <input type="number" id="poke-cooldown" class="input input-sm settings-default-input font-mono" style="width: 6rem; flex: none;" min="0" max="86400" step="1" placeholder="30" />
                <span class="text-xs text-muted">秒</span>
              </div>
              <p class="text-[11px] text-muted">每个群/私聊独立冷却，同一群内所有人共享。默认 30 秒，0 表示不冷却；冷却期间不会向 LLM 发送消息。</p>
            </div>
            <p class="text-[11px] text-muted mt-2">此行为会被雪花计费。</p>
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
                    class="input input-sm settings-default-input font-mono text-right"
                    min="0.01"
                    max="1.00"
                    step="0.01"
                    placeholder="0.01"
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
                  启用后，非白名单群不会触发主动回复；正常被唤醒的回复不受此限制。
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

          <!-- Bot loop guard -->
          <div class="mb-4 pb-4 border-b border-border">
            <h3 class="text-xs font-semibold text-foreground mb-2.5">Bot 连续对话防循环</h3>
            <p class="text-[11px] text-muted mb-3">按会话分别累计各标记 Bot 的轮数；仅未标记用户明确唤醒（@、名称/别名或戳一戳）后恢复额度，普通闲聊不会重置。</p>
            <div class="grid grid-cols-1 sm:grid-cols-[1fr_auto_auto] gap-3 items-end">
              <div class="flex flex-col gap-1.5 min-w-0">
                <label for="bot-loop-user-id-input" class="text-xs font-medium">标记为 Bot 的用户 ID</label>
                <div class="flex items-center gap-2">
                  <input id="bot-loop-user-id-input" type="text" class="input input-sm font-mono text-xs flex-1 min-w-0" placeholder="输入用户 ID" autocomplete="off" />
                  <button type="button" id="bot-loop-add-user" class="btn btn-outline btn-sm shrink-0">添加</button>
                </div>
              </div>
              <label class="flex flex-col gap-1.5">
                <span class="text-xs font-medium">每个 Bot 最多轮数</span>
                <input id="bot-loop-max-turns" type="number" class="input input-sm settings-default-input font-mono text-xs w-28" min="1" step="1" placeholder="3" />
              </label>
              <label class="flex flex-col gap-1.5">
                <span class="text-xs font-medium">超限处理</span>
                <select id="bot-loop-mode" class="input input-sm settings-default-select text-xs">
                  <option value="">默认（提示静默）</option>
                  <option value="soft">非强制：提示静默</option>
                  <option value="hard">强制：丢弃请求</option>
                </select>
              </label>
            </div>
            <div id="bot-loop-user-tags" class="flex flex-wrap gap-1.5 mt-2" aria-live="polite"></div>
            <div class="flex items-center justify-between gap-2 mt-2">
              <span class="text-[11px] text-muted">列表为空即关闭。可用 platform:ID 只标记指定平台；裸 ID 对所有平台生效。</span>
            </div>
          </div>

          <div class="mb-4 pb-4 border-b border-border" id="sticker-activity-card">
            <div class="flex items-center justify-between mb-3 gap-2">
              <h3 class="text-xs font-semibold text-foreground">表情包使用积极度</h3>
              <div class="flex items-center gap-2">
                <span class="text-xs font-mono font-semibold text-primary" id="sticker-activity-display" aria-live="polite">0 · 已禁用</span>
                <input type="number" id="sticker-activity-number" class="input input-sm settings-default-input w-16 text-right" min="0" max="10" step="1" placeholder="0" aria-label="表情包使用积极度数值" />
              </div>
            </div>
            <label for="sticker-activity-mode" class="text-xs font-medium text-foreground">计数方式</label>
            <select id="sticker-activity-mode" class="select settings-default-select w-full mt-1 mb-2">
              <option value="">默认（按 Bot 对话轮次）</option>
              <option value="bot_turns">按 Bot 对话轮次</option>
              <option value="all_messages">按收到的消息条数</option>
            </select>
            <p id="sticker-activity-mode-description" class="text-[11px] text-muted mb-3">每次唤醒或触发主动回复计一轮。</p>
            <p class="text-[11px] text-muted mb-3">积极度控制发送约束的间隔，不是概率；计数在实例内共享，不按群分别计数。</p>
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
                <span class="toggle-switch"><input type="checkbox" id="group-mention-cb" role="switch" aria-label="启用群聊对话回复" /><span aria-hidden="true"></span></span>
                <div>
                  <span class="text-xs font-semibold text-foreground">启用群聊对话回复</span>
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
            <p class="text-[11px] text-muted mt-2">关闭群聊对话回复后，@、名称或别名唤醒及主动回复均不响应。</p>
          </div>

          <div class="mt-4 p-3.5 rounded-lg border border-border flex flex-col gap-3">
            <div class="flex items-center justify-between gap-2.5">
              <span class="text-xs font-semibold text-foreground">引用追溯</span>
              <label class="toggle-switch" title="引用追溯">
                <input type="checkbox" id="quote-trace-cb" role="switch" aria-label="引用追溯" />
                <span aria-hidden="true"></span>
              </label>
            </div>
            <div class="flex items-center gap-2">
              <label for="quote-trace-depth" class="text-xs text-muted">追溯深度</label>
              <input type="number" id="quote-trace-depth" class="input input-sm settings-default-input font-mono w-20"
                min="1" max="10" step="1" placeholder="5" />
              <span class="text-xs text-muted">层（1～10，默认 5）</span>
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
            <button id="extra-env-add" type="button" class="btn btn-outline btn-sm">添加</button>
          </div>
        </article>
      </div>

    </div>
  `;

  const element = <T extends HTMLElement>(id: string) => container.querySelector<T>('#' + id)!;
  const refreshBtn = element<HTMLButtonElement>('backend-refresh-btn');
  const banner = element<HTMLElement>('settings-draft-banner');
  const restoreBtn = element<HTMLButtonElement>('settings-draft-restore');
  const saveBtn = element<HTMLButtonElement>('settings-draft-save');
  const settingsRoot = element<HTMLElement>('settings-form');
  const extraList = element<HTMLElement>('extra-env-list');
  const extraKey = element<HTMLInputElement>('extra-env-key');
  const extraValue = element<HTMLInputElement>('extra-env-value');
  const extraAdd = element<HTMLButtonElement>('extra-env-add');
  const pokeEnabled = element<HTMLInputElement>('poke-enabled');
  const pokeText = element<HTMLInputElement>('poke-custom-text');
  const pokeCooldown = element<HTMLInputElement>('poke-cooldown');
  function updatePokePreview(): void {
    const text = pokeText.value.trim();
    element<HTMLElement>('poke-preview').textContent = '发送预览：user:（用户戳了戳你' + (text ? ' ' + text : '') + '）';
  }
  function updatePokeControls(): void {
    pokeText.disabled = pokeCooldown.disabled = !pokeEnabled.checked;
    element<HTMLElement>('poke-controls').style.opacity = pokeEnabled.checked ? '1' : '0.55';
  }
  const proactive = element<HTMLInputElement>('proactive-reply-cb');
  const probability = element<HTMLInputElement>('proactive-reply-number');
  const probabilitySlider = element<HTMLInputElement>('proactive-reply-slider');
  const whitelist = element<HTMLInputElement>('proactive-whitelist-cb');
  const groupSelect = element<HTMLSelectElement>('proactive-whitelist-select');
  const groupInput = element<HTMLInputElement>('proactive-whitelist-input');
  const groupAdd = element<HTMLButtonElement>('proactive-whitelist-add-btn');
  const groupTags = element<HTMLElement>('proactive-whitelist-tags');
  const botUserInput = element<HTMLInputElement>('bot-loop-user-id-input');
  const botUserAdd = element<HTMLButtonElement>('bot-loop-add-user');
  const botUserTags = element<HTMLElement>('bot-loop-user-tags');
  const stickerSlider = element<HTMLInputElement>('sticker-activity-slider');
  const stickerNumber = element<HTMLInputElement>('sticker-activity-number');
  const stickerMode = element<HTMLSelectElement>('sticker-activity-mode');
  const quote = element<HTMLInputElement>('quote-trace-cb');
  const quoteDepth = element<HTMLInputElement>('quote-trace-depth');
  const embeddingToggle = element<HTMLInputElement>('embedding-enabled');
  const embeddingWarning = element<HTMLElement>('embedding-sqlite-warning');
  function updateEmbeddingWarning(): void {
    embeddingWarning.hidden = !(embeddingToggle.checked && storageBackend === 'sqlite');
  }
  const numericControls: Record<string, HTMLInputElement> = {
    POKE_COOLDOWN_SECONDS: pokeCooldown,
    PROACTIVE_REPLY_PROBABILITY: probability, STICKER_ACTIVITY: stickerNumber,
    QUOTE_TRACE_DEPTH: quoteDepth, BOT_LOOP_MAX_TURNS: element<HTMLInputElement>('bot-loop-max-turns'),
  };

  function invalidNumbers(): HTMLInputElement[] {
    return [...container.querySelectorAll<HTMLInputElement>('input[type="number"]')].filter((input) => input.validity?.badInput);
  }
  function dirty(): boolean { return !!draft?.dirty || !!extraKey.value || !!extraValue.value || !!groupInput.value || !!botUserInput.value || invalidNumbers().length > 0; }
  function updateBanner(): void {
    banner.hidden = !dirty();
    saveBtn.disabled = saving || loading || !draft;
    restoreBtn.disabled = saving || loading;
    saveBtn.textContent = saving ? '保存中…' : '保存';
    refreshBtn.disabled = saving || loading || dirty();
  }

  function change(key: string, value: string): void {
    if (!draft || saving || loading) return;
    draft.set(key, value);
    updateBanner();
  }

  function renderForm(): void {
    if (!draft) {
      settingsRoot.innerHTML = '<div class="card p-8 text-center text-muted">加载设置后即可编辑</div>';
      return;
    }
    renderBackendSettingsForm(settingsRoot, draft.values(), (key) => globalKeys.has(key), change);
  }

  function renderGroups(): void {
    const groups = parseWhitelistGroups(draft?.get('PROACTIVE_REPLY_GROUP_WHITELIST') ?? '');
    whitelist.checked = isWhitelistEnabled(draft?.get('ENABLE_PROACTIVE_REPLY_WHITELIST'), groups.length);
    groupTags.innerHTML = groups.length === 0 ? '<span class="text-[11px] text-muted">白名单为空。</span>' : groups.map((id) => `
      <span class="inline-flex items-center gap-1.5 px-2 py-0.5 rounded bg-muted border border-border text-xs">
        ${escapeHtml(formatGroupOption(id, sessionGroupMap.get(id)))}
        <button type="button" data-group-id="${escapeHtml(id)}" aria-label="移除群聊" class="proactive-remove-group-btn">×</button>
      </span>`).join('');
    groupTags.querySelectorAll<HTMLButtonElement>('[data-group-id]').forEach((button) => {
      button.addEventListener('click', () => {
        changeGroups(groups.filter((id) => id !== button.dataset.groupId));
      });
    });
  }

  function changeGroups(groups: string[]): void {
    if (!draft) return;
    if (!draft.get('ENABLE_PROACTIVE_REPLY_WHITELIST')) {
      change('ENABLE_PROACTIVE_REPLY_WHITELIST', whitelist.checked ? 'true' : 'false');
    }
    change('PROACTIVE_REPLY_GROUP_WHITELIST', groups.join(','));
    renderGroups();
  }

  function normalizeBotUserID(userID: string): string {
    const colon = userID.indexOf(':');
    return colon < 0 ? userID : userID.slice(0, colon).toLowerCase() + userID.slice(colon);
  }

  function botUserIDs(): string[] {
    return [...new Set((draft?.get('BOT_LOOP_USER_IDS') ?? '').split(/[,;\s]+/).filter(Boolean).map(normalizeBotUserID))];
  }

  function renderBotUsers(): void {
    const ids = botUserIDs();
    const disabled = !draft || saving || loading;
    botUserTags.innerHTML = ids.length === 0
      ? '<span class="text-[11px] text-muted">尚未添加用户 ID</span>'
      : ids.map((userID, index) => `
          <span class="inline-flex items-center gap-1.5 px-2 py-0.5 rounded bg-muted border border-border text-xs text-foreground font-mono min-w-0">
            <span class="break-all">${escapeHtml(userID)}</span>
            <button type="button" class="text-muted hover:text-danger shrink-0 cursor-pointer" data-bot-user-index="${index}" aria-label="移除 ${escapeHtml(userID)}" title="移除用户 ID" ${disabled ? 'disabled' : ''}>&times;</button>
          </span>`).join('');
    botUserTags.querySelectorAll<HTMLButtonElement>('[data-bot-user-index]').forEach((button) => {
      button.addEventListener('click', () => {
        if (!draft || saving || loading) return;
        change('BOT_LOOP_USER_IDS', ids.filter((_, index) => index !== Number(button.dataset.botUserIndex)).join(','));
        renderBotUsers();
      });
    });
  }

  function addBotUser(): boolean {
    if (!draft || saving || loading) return false;
    const userID = normalizeBotUserID(botUserInput.value.trim());
    if (!userID || /[,;\s]/.test(userID)) {
      toast.error(userID ? '每次添加一个用户 ID' : '请输入用户 ID');
      return false;
    }
    const ids = botUserIDs();
    if (ids.includes(userID)) {
      toast.error('该用户 ID 已添加');
      return false;
    }
    botUserInput.value = '';
    change('BOT_LOOP_USER_IDS', [...ids, userID].join(','));
    renderBotUsers();
    botUserInput.focus();
    return true;
  }

  function updateProbabilityDisplay(): void {
    const text = probability.value || '0.01';
    const value = Number(text);
    element<HTMLElement>('proactive-reply-prob-display').textContent = Number.isFinite(value)
      ? `${text} (${Math.round(value * 100)}%)` : '待填写';
  }

  function updateStickerDisplay(): void {
    const level = Number(stickerNumber.value);
    element<HTMLElement>('sticker-activity-display').textContent = level === 0 ? '0 · 已禁用' : `${level} · 每 ${(11 - level) * 5} 轮`;
    element<HTMLElement>('sticker-activity-mode-description').textContent = stickerMode.value === 'all_messages'
      ? '每条传入消息计一轮，无需唤醒 Bot。' : '每次唤醒或触发主动回复计一轮。';
  }

  function renderControls(): void {
    if (!draft) return;
    const value = (key: string) => draft!.get(key);
    pokeEnabled.checked = value('ENABLE_POKE') === 'true';
    pokeText.value = value('POKE_CUSTOM_TEXT');
    pokeCooldown.value = value('POKE_COOLDOWN_SECONDS');
    updatePokePreview();
    updatePokeControls();
    embeddingToggle.checked = value('ENABLE_EMBEDDING') === 'true';
    updateEmbeddingWarning();
    proactive.checked = value('ENABLE_PROACTIVE_REPLY') === 'true' ||
      (value('ENABLE_PROACTIVE_REPLY') !== 'false' && Number(value('PROACTIVE_REPLY_PROBABILITY')) > 0);
    probability.value = value('PROACTIVE_REPLY_PROBABILITY').trim();
    probabilitySlider.value = probability.value || '0.01';
    probability.disabled = probabilitySlider.disabled = !proactive.checked;
    element<HTMLElement>('proactive-reply-controls').style.opacity = proactive.checked ? '1' : '0.55';
    element<HTMLElement>('proactive-reply-status-text').style.display = 'none';
    element<HTMLElement>('proactive-whitelist-status-text').style.display = 'none';
    const toggles: Record<string, string> = {
      'group-mention-cb': 'GROUP_REPLY_ON_MENTION', 'group-at-cb': 'ENABLE_AT_IN_GROUP_MSG',
      'group-reply-cb': 'ENABLE_REPLY_IN_GROUP_MSG',
    };
    for (const [id, key] of Object.entries(toggles)) {
      element<HTMLInputElement>(id).checked = key === 'GROUP_REPLY_ON_MENTION' ? value(key) !== 'false' : value(key) === 'true';
    }
    quote.checked = value('ENABLE_QUOTE_TRACE') !== 'false';
    quoteDepth.value = value('QUOTE_TRACE_DEPTH');
    quoteDepth.disabled = !quote.checked;
    renderBotUsers();
    element<HTMLInputElement>('bot-loop-max-turns').value = value('BOT_LOOP_MAX_TURNS');
    const loopMode = element<HTMLSelectElement>('bot-loop-mode');
    loopMode.value = value('BOT_LOOP_MODE');
    loopMode.dataset.default = String(!loopMode.value);
    stickerNumber.value = value('STICKER_ACTIVITY');
    stickerSlider.value = stickerNumber.value || '0';
    stickerMode.value = value('STICKER_ACTIVITY_MODE');
    stickerMode.dataset.default = String(!stickerMode.value);
    renderGroups();
    updateProbabilityDisplay();
    updateStickerDisplay();
  }

  function validateExtra(key: string, value: string): void {
    if (key.length > 128 || !/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) throw new Error('请填写有效的环境变量名');
    const upper = key.toUpperCase();
    if (settingFields[upper] || dedicatedSettingKeys.has(upper) || draft?.values().some((item) => item.key === upper) ||
      ['BRAIN_PATH', 'DIALOGUE_PATH', 'ONEBOT_WS_PATH', 'ASTRBOT_WS_PATH'].includes(upper)) {
      throw new Error('此项应在常规设置中修改');
    }
    if (new TextEncoder().encode(value).length > 16 * 1024 || /[\r\n\0]/.test(value)) {
      throw new Error('环境变量值不得超过 16 KiB，也不能包含换行或 NUL');
    }
  }

  function addExtra(): boolean {
    if (!draft) return false;
    try {
      const key = extraKey.value.trim();
      validateExtra(key, extraValue.value);
      draft.setExtra(key, extraValue.value);
      extraKey.value = extraValue.value = '';
      renderExtra();
      updateBanner();
      return true;
    } catch (error) {
      toast.error(String(error));
      return false;
    }
  }

  function renderExtra(): void {
    const entries = draft?.extraEntries() ?? [];
    extraList.innerHTML = entries.length === 0 ? '<p class="text-xs text-muted">暂无额外注入项。</p>' : entries.map(({ key, value }) => `
      <div class="flex items-center gap-2 min-w-0" data-extra-key="${escapeHtml(key)}">
        <span class="font-mono text-xs w-40 shrink-0 break-all">${escapeHtml(key)}</span>
        <input class="input input-sm flex-1 min-w-0" type="password" value="${escapeHtml(value)}" aria-label="${escapeHtml(key)}的值" />
        <button type="button" class="btn btn-outline btn-sm" data-extra-show>显示</button>
        <button type="button" class="btn btn-ghost btn-sm text-destructive" data-extra-delete>删除</button>
      </div>`).join('');
    extraList.querySelectorAll<HTMLElement>('[data-extra-key]').forEach((row) => {
      const key = row.dataset.extraKey!;
      const input = row.querySelector<HTMLInputElement>('input')!;
      input.addEventListener('input', () => { draft!.setExtra(key, input.value); updateBanner(); });
      row.querySelector<HTMLButtonElement>('[data-extra-show]')!.addEventListener('click', (event) => {
        input.type = input.type === 'password' ? 'text' : 'password';
        (event.currentTarget as HTMLButtonElement).textContent = input.type === 'password' ? '显示' : '隐藏';
      });
      row.querySelector<HTMLButtonElement>('[data-extra-delete]')!.addEventListener('click', () => {
        draft!.deleteExtra(key);
        renderExtra();
        updateBanner();
      });
    });
  }

  function renderAll(): void {
    container.querySelectorAll<HTMLInputElement>('input, select, button').forEach((input) => { input.disabled = false; });
    renderForm();
    renderControls();
    renderExtra();
    if (saving || loading || !draft) container.querySelectorAll<HTMLInputElement>('input, select, button').forEach((input) => { input.disabled = true; });
    refreshBtn.disabled = loading || saving || dirty();
    updateBanner();
  }

  async function loadData(): Promise<void> {
    const sequence = ++loadSeq;
    loading = true;
    renderAll();
    try {
      const [memorySettings, extra, sessions] = await Promise.all([
        api.listEnvVarsWithStorage(), extraAPI.list(), api.getSessions(100).catch(() => ({ sessions: [] })),
      ]);
      if (isUnmounted || sequence !== loadSeq) return;
      storageBackend = memorySettings.storageBackend;
      draft = new SettingsDraft(memorySettings.envVars, extra);
      sessionGroupMap.clear();
      for (const session of sessions.sessions ?? []) {
        const id = groupIdFromSessionId(session.sessionId);
        if (id) sessionGroupMap.set(id, session.groupName || '');
      }
      groupSelect.innerHTML = '<option value="">-- 从最近会话选择群聊 --</option>' +
        [...sessionGroupMap].map(([id, name]) => `<option value="${escapeHtml(id)}">${escapeHtml(formatGroupOption(id, name))}</option>`).join('');
    } catch (error) {
      if (!isUnmounted) toast.error('加载设置失败: ' + String(error));
    } finally {
      if (!isUnmounted && sequence === loadSeq) { loading = false; renderAll(); }
    }
  }

  async function saveDraft(): Promise<void> {
    if (!draft || saving || loading) return;
    if (botUserInput.value && !addBotUser()) return;
    if (groupInput.value) addGroup();
    if ((extraKey.value || extraValue.value) && !addExtra()) return;
    for (const input of invalidNumbers()) {
      const disabled = input.disabled;
      input.disabled = false;
      input.reportValidity();
      input.disabled = disabled;
      return;
    }
    for (const { key, value } of draft.pending()) {
      if (value === null) continue;
      const input = numericControls[key] ?? container.querySelector<HTMLInputElement>(`input[type="number"][data-setting-input="${key}"]`);
      if (!input) continue;
      const disabled = input.disabled;
      if (value !== '' && settingFields[key]?.positive && !(Number(value) > 0)) {
        toast.error(`${settingFields[key].label}必须大于 0`);
        return;
      }
      input.disabled = false;
      const valid = input.reportValidity();
      input.disabled = disabled;
      if (!valid) return;
    }
    const current = draft;
    const extraChanges = current.extraChanges();
    try {
      extraChanges.forEach(({ key, value }) => validateExtra(key, value ?? ''));
    } catch (error) { toast.error(String(error)); return; }
    const changes = current.pending();
    saving = true;
    renderAll();
    try {
      for (const { key, value } of extraChanges) {
        controller.signal.throwIfAborted();
        if (value === null) await extraAPI.delete(key, controller.signal);
        else await extraAPI.put({ key, value }, controller.signal);
        current.acknowledgeExtra(key, value);
      }
      if (changes.length) {
        controller.signal.throwIfAborted();
        const response = await api.updateEnvVars(changes, controller.signal);
        if (!response.success) throw new Error(response.error);
        current.acknowledgeSettings(changes);
        if (changes.some(({ key }) => key === 'MCP_CONTROL_TOKEN' || key === 'ADMIN_TOKEN')) {
          setControlToken(current.get('MCP_CONTROL_TOKEN').trim() || current.get('ADMIN_TOKEN'));
        }
      }
      if (isUnmounted) return;
      toast.success(extraChanges.length ? '设置已保存；额外 env 注入项重启 FrostAgent 后生效' : '设置已保存');
      saving = false;
      await loadData();
    } catch (error) {
      if (!isUnmounted) toast.error('保存未完成，剩余更改仍保留在草稿中: ' + String(error));
    } finally {
      saving = false;
      if (!isUnmounted) renderAll();
    }
  }

  const binds: Record<string, string> = {
    'poke-enabled': 'ENABLE_POKE',
    'embedding-enabled': 'ENABLE_EMBEDDING',
    'group-mention-cb': 'GROUP_REPLY_ON_MENTION', 'group-at-cb': 'ENABLE_AT_IN_GROUP_MSG',
    'group-reply-cb': 'ENABLE_REPLY_IN_GROUP_MSG', 'quote-trace-cb': 'ENABLE_QUOTE_TRACE',
    'proactive-whitelist-cb': 'ENABLE_PROACTIVE_REPLY_WHITELIST',
  };
  for (const [id, key] of Object.entries(binds)) {
    const input = element<HTMLInputElement>(id);
    input.addEventListener('change', () => {
      change(key, input.checked ? 'true' : 'false');
      if (input === pokeEnabled) updatePokeControls();
      if (input === embeddingToggle) updateEmbeddingWarning();
      if (input === quote) quoteDepth.disabled = !quote.checked;
    });
  }
  pokeText.addEventListener('input', () => {
    change('POKE_CUSTOM_TEXT', pokeText.value);
    updatePokePreview();
  });
  const texts: Record<string, string> = {
    'poke-cooldown': 'POKE_COOLDOWN_SECONDS',
    'bot-loop-max-turns': 'BOT_LOOP_MAX_TURNS', 'quote-trace-depth': 'QUOTE_TRACE_DEPTH',
  };
  for (const [id, key] of Object.entries(texts)) {
    const input = element<HTMLInputElement>(id);
    input.addEventListener('input', () => change(key, input.value));
  }
  element<HTMLSelectElement>('bot-loop-mode').addEventListener('change', () => {
    const input = element<HTMLSelectElement>('bot-loop-mode');
    input.dataset.default = String(!input.value);
    change('BOT_LOOP_MODE', input.value);
  });
  proactive.addEventListener('change', () => {
    change('ENABLE_PROACTIVE_REPLY', proactive.checked ? 'true' : 'false');
    const rawProbability = draft?.get('PROACTIVE_REPLY_PROBABILITY') ?? '';
    const editedProbability = draft?.pending().some(({ key }) => key === 'PROACTIVE_REPLY_PROBABILITY');
    const trimmedProbability = rawProbability.trim();
    const numericProbability = /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(trimmedProbability)
      ? Number(trimmedProbability) : Number.NaN;
    if (proactive.checked && !editedProbability && trimmedProbability &&
      (!Number.isFinite(numericProbability) || numericProbability <= 0)) {
      change('PROACTIVE_REPLY_PROBABILITY', '');
      probability.value = '';
      probabilitySlider.value = '0.01';
      updateProbabilityDisplay();
    }
    probability.disabled = probabilitySlider.disabled = !proactive.checked;
    element<HTMLElement>('proactive-reply-controls').style.opacity = proactive.checked ? '1' : '0.55';
  });
  probabilitySlider.addEventListener('input', () => {
    probability.value = probabilitySlider.value;
    change('PROACTIVE_REPLY_PROBABILITY', probability.value);
    updateProbabilityDisplay();
  });
  probability.addEventListener('input', () => {
    probabilitySlider.value = probability.value || '0.01';
    change('PROACTIVE_REPLY_PROBABILITY', probability.value);
    updateProbabilityDisplay();
  });
  stickerSlider.addEventListener('input', () => {
    stickerNumber.value = stickerSlider.value;
    change('STICKER_ACTIVITY', stickerSlider.value);
    updateStickerDisplay();
  });
  stickerNumber.addEventListener('input', () => {
    stickerSlider.value = stickerNumber.value || '0';
    change('STICKER_ACTIVITY', stickerNumber.value);
    updateStickerDisplay();
  });
  stickerMode.addEventListener('change', () => {
    stickerMode.dataset.default = String(!stickerMode.value);
    change('STICKER_ACTIVITY_MODE', stickerMode.value);
    updateStickerDisplay();
  });
  groupSelect.addEventListener('change', () => { groupInput.value = groupSelect.value; updateBanner(); });
  const addGroup = () => {
    if (!groupInput.value.trim()) return;
    const groups = parseWhitelistGroups((draft?.get('PROACTIVE_REPLY_GROUP_WHITELIST') ?? '') + ',' + groupInput.value);
    changeGroups(groups);
    groupInput.value = groupSelect.value = '';
    updateBanner();
  };
  groupAdd.addEventListener('click', addGroup);
  groupInput.addEventListener('input', updateBanner);
  groupInput.addEventListener('keydown', (event) => { if (event.key === 'Enter') { event.preventDefault(); addGroup(); } });
  botUserAdd.addEventListener('click', () => { addBotUser(); });
  botUserInput.addEventListener('input', updateBanner);
  botUserInput.addEventListener('keydown', (event) => {
    if (event.key === 'Enter' && !event.isComposing) { event.preventDefault(); addBotUser(); }
  });
  extraKey.addEventListener('input', updateBanner);
  extraValue.addEventListener('input', updateBanner);
  extraAdd.addEventListener('click', addExtra);
  restoreBtn.addEventListener('click', () => {
    if (saving) return;
    draft?.restore();
    extraKey.value = extraValue.value = '';
    groupInput.value = groupSelect.value = '';
    botUserInput.value = '';
    renderAll();
  });
  saveBtn.addEventListener('click', () => void saveDraft());
  refreshBtn.addEventListener('click', () => { if (!dirty()) void loadData(); });
  void loadData();
  return () => { isUnmounted = true; controller.abort(); draft = null; banner.hidden = true; };
}
