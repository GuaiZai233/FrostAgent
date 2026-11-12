import { escapeHtml } from '../utils/formatters';
import { formatGroupOption, isWhitelistEnabled, parseWhitelistGroups } from './proactive-whitelist';

export const awarenessKeys = [
  'ENABLE_PROACTIVE_AWARENESS', 'PROACTIVE_AWARENESS_INTERVAL',
  'ENABLE_PROACTIVE_AWARENESS_PRIVATE_WHITELIST', 'PROACTIVE_AWARENESS_PRIVATE_WHITELIST',
  'ENABLE_PROACTIVE_AWARENESS_GROUP_WHITELIST', 'PROACTIVE_AWARENESS_GROUP_WHITELIST',
] as const;

export function validAwarenessInterval(raw: string): boolean {
  if (!raw.trim()) return true;
  const match = /^\s*(\d+(?:\.\d+)?)([mh])\s*-\s*(\d+(?:\.\d+)?)([mh])\s*$/.exec(raw);
  if (!match) return false;
  const lo = Number(match[1]) * (match[2] === 'h' ? 60 : 1);
  const hi = Number(match[3]) * (match[4] === 'h' ? 60 : 1);
  // Go durations are signed nanoseconds. Keep the client validation aligned.
  return lo > 0 && hi >= lo && hi <= Number((1n << 63n) - 1n) / 60e9;
}

interface AwarenessSettingsOptions {
  get: (key: string) => string | undefined;
  change: (key: string, value: string) => void;
  canEdit: () => boolean;
  groups: ReadonlyMap<string, string>;
  privateIDs: readonly string[];
  dirty: () => void;
}

export function mountAwarenessSettings(root: HTMLElement, options: AwarenessSettingsOptions): () => boolean {
  const kinds = ['private', 'group'] as const;
  const listKey = (kind: string) => `PROACTIVE_AWARENESS_${kind.toUpperCase()}_WHITELIST`;
  const switchKey = (kind: string) => `ENABLE_${listKey(kind)}`;
  const get = (key: string) => options.get(key) ?? '';
  function setIDs(kind: string, ids: string[]): void {
    const currentSwitch = get(switchKey(kind)).trim().toLowerCase();
    if (currentSwitch !== 'true' && currentSwitch !== 'false') {
      options.change(switchKey(kind), String(isWhitelistEnabled(currentSwitch, parseWhitelistGroups(get(listKey(kind))).length)));
    }
    options.change(listKey(kind), ids.join(','));
  }
  root.innerHTML = `<article class="card p-4" id="awareness-card">
    <div class="flex items-start justify-between gap-4 mb-4">
      <div><h2 class="card-title text-sm font-semibold">主动感知</h2>
      <p class="text-xs text-muted mt-1">不定期向Bot自动注入提示词，使其具备主动给用户发送私聊消息/主动开启话题的能力。</p></div>
      <label class="toggle-switch"><input id="awareness-enabled" type="checkbox" role="switch" aria-label="主动感知" ${get(awarenessKeys[0]) === 'true' ? 'checked' : ''}><span aria-hidden="true"></span></label>
    </div>
    <label for="awareness-interval" class="text-xs font-semibold">活跃阈值</label>
    <input id="awareness-interval" class="input input-sm mt-2 w-full font-mono" value="${escapeHtml(get(awarenessKeys[1]))}" placeholder="30m - 2h" aria-describedby="awareness-interval-hint">
    <p id="awareness-interval-hint" class="text-xs text-muted mt-1">例如 30m - 2h：每个会话每隔 30 分钟到 2 小时随机触发一次逻辑。时长以 m 或 h 结尾；正在聊天也会触发，普通消息不重置计时。保存后生效。</p>
    <p class="text-xs text-muted mt-2">仅在实例运行且发送连接可用时计时；重载后重新计时。触发后由 Bot 结合上下文决定内容。</p>
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-4 mt-4">${kinds.map((kind) => {
      const label = kind === 'private' ? '私聊' : '群聊';
      const ids = parseWhitelistGroups(get(listKey(kind)));
      return `<section class="rounded-lg border border-border p-3" data-awareness-kind="${kind}">
        <div class="flex justify-between items-center gap-2 mb-2"><h3 class="text-xs font-semibold">${label}白名单</h3>
        <label class="toggle-switch"><input data-awareness-switch="${kind}" type="checkbox" role="switch" aria-label="主动感知${label}白名单" ${isWhitelistEnabled(get(switchKey(kind)), ids.length) ? 'checked' : ''}><span aria-hidden="true"></span></label></div>
        <p class="text-xs text-muted mb-2">开启后仅触发名单内的${label}，名单为空则不触发；关闭后适用于已知${label}。</p>
        <select class="input input-sm w-full mb-2" data-awareness-select="${kind}" aria-label="选择主动感知${label}">
          <option value="">从已知${label}选择</option>
          ${(kind === 'group' ? [...options.groups].map(([id, name]) => [id, formatGroupOption(id, name)]) : options.privateIDs.map((id) => [id, id])).map(([id, name]) => `<option value="${escapeHtml(id)}">${escapeHtml(name)}</option>`).join('')}
        </select>
        <div class="flex gap-2"><input class="input input-sm flex-1 min-w-0" data-awareness-input="${kind}" aria-label="主动感知${label} ID" placeholder="QQ ID 或 platform:ID"><button type="button" class="btn btn-secondary btn-sm" data-awareness-add="${kind}">添加</button></div>
        <div class="flex flex-wrap gap-1.5 mt-2" data-awareness-tags="${kind}"></div>
      </section>`;
    }).join('')}</div>
  </article>`;
  root.querySelector<HTMLInputElement>('#awareness-enabled')!.addEventListener('change', (event) => {
    if (options.canEdit()) options.change(awarenessKeys[0], String((event.target as HTMLInputElement).checked));
  });
  const interval = root.querySelector<HTMLInputElement>('#awareness-interval')!;
  interval.addEventListener('input', () => {
    interval.setCustomValidity(validAwarenessInterval(interval.value) ? '' : '请输入有效区间，例如 30m - 2h，下限不得大于上限。');
    if (options.canEdit()) options.change(awarenessKeys[1], interval.value);
  });
  function renderTags(kind: string): void {
    const tags = root.querySelector<HTMLElement>(`[data-awareness-tags="${kind}"]`)!;
    const ids = parseWhitelistGroups(get(listKey(kind)));
    tags.innerHTML = ids.length ? ids.map((id, index) => `<span class="inline-flex items-center gap-1 rounded border border-border bg-muted px-2 py-0.5 text-xs max-w-full"><span class="break-all">${escapeHtml(kind === 'group' ? formatGroupOption(id, options.groups.get(id)) : id)}</span><button type="button" data-awareness-remove="${index}" aria-label="移除 ${escapeHtml(id)}">×</button></span>`).join('') : '<span class="text-xs text-muted">白名单为空</span>';
    tags.querySelectorAll<HTMLButtonElement>('[data-awareness-remove]').forEach((button) => button.addEventListener('click', () => {
      if (!options.canEdit()) return;
      setIDs(kind, ids.filter((_, i) => i !== Number(button.dataset.awarenessRemove)));
      renderTags(kind);
    }));
  }
  function add(kind: string): boolean {
    const input = root.querySelector<HTMLInputElement>(`[data-awareness-input="${kind}"]`)!;
    if (!input.value.trim()) return true;
    if (!options.canEdit()) return false;
    const id = input.value.trim();
    if (/[,;\s]/.test(id) || id.startsWith(':') || id.endsWith(':')) {
      input.setCustomValidity('每次添加一个有效 ID'); input.reportValidity(); return false;
    }
    setIDs(kind, [...new Set([...parseWhitelistGroups(get(listKey(kind))), id])]);
    input.value = ''; input.setCustomValidity(''); renderTags(kind); options.dirty();
    return true;
  }
  for (const kind of kinds) {
    renderTags(kind);
    root.querySelector<HTMLInputElement>(`[data-awareness-switch="${kind}"]`)!.addEventListener('change', (event) => {
      if (options.canEdit()) options.change(switchKey(kind), String((event.target as HTMLInputElement).checked));
    });
    const input = root.querySelector<HTMLInputElement>(`[data-awareness-input="${kind}"]`)!;
    input.addEventListener('input', () => { input.setCustomValidity(''); options.dirty(); });
    input.addEventListener('keydown', (event) => { if (event.key === 'Enter' && !event.isComposing) { event.preventDefault(); add(kind); } });
    root.querySelector(`[data-awareness-add="${kind}"]`)!.addEventListener('click', () => add(kind));
    root.querySelector<HTMLSelectElement>(`[data-awareness-select="${kind}"]`)!.addEventListener('change', (event) => {
      const select = event.target as HTMLSelectElement;
      if (select.value) { input.value = select.value; add(kind); select.value = ''; }
    });
  }
  if (!options.canEdit()) root.querySelectorAll<HTMLInputElement | HTMLSelectElement | HTMLButtonElement>('input, select, button').forEach((input) => { input.disabled = true; });
  return () => {
    if (!validAwarenessInterval(interval.value)) { interval.setCustomValidity('请输入有效区间，例如 30m - 2h。'); interval.reportValidity(); return false; }
    return kinds.every(add);
  };
}
