import { escapeHtml } from '../utils/formatters';
import { icon } from '../components/icons';
import { formatGroupOption, parseWhitelistGroups } from './proactive-whitelist';
import {
  COMPACT_WHITELIST_ENABLED_KEY,
  COMPACT_WHITELIST_GROUPS_KEY,
  planCompactGroupChange,
} from './compact-whitelist-plan';

export interface CompactWhitelistDeps {
  get: (key: string) => string | undefined;
  change: (key: string, value: string) => void;
  canEdit: () => boolean;
  sessionGroups: () => Map<string, string>;
  effectiveEnabled: (enabledRaw: string | undefined, groupCount: number) => boolean;
}

/** Renders the group compact whitelist card into `parent` and wires its controls. */
export function mountGroupCompactWhitelist(parent: HTMLElement, deps: CompactWhitelistDeps): void {
  const enabledRaw = deps.get(COMPACT_WHITELIST_ENABLED_KEY);
  const groups = parseWhitelistGroups(deps.get(COMPACT_WHITELIST_GROUPS_KEY) ?? '');
  const enabled = deps.effectiveEnabled(enabledRaw, groups.length);
  const names = deps.sessionGroups();
  const label = (id: string) => formatGroupOption(id, names.get(id));

  parent.innerHTML = `
    <div class="col-span-full min-w-0 pt-3 border-t border-border/60 flex flex-col gap-2.5" id="compact-whitelist">
      <div class="flex items-center justify-between flex-wrap gap-2">
        <span class="text-xs font-semibold text-foreground">群聊压缩白名单模式</span>
        <label class="toggle-switch" title="群聊压缩白名单模式">
          <input type="checkbox" id="compact-whitelist-cb" role="switch" aria-label="群聊压缩白名单模式" ${enabled ? 'checked' : ''} />
          <span aria-hidden="true"></span>
        </label>
      </div>
      <p class="text-[11px] text-muted">
        启用后仅白名单群触发群聊压缩。其余群不压缩、不注入群聊总结，但仍保留未压缩的最近群消息并正常被唤醒；白名单为空时所有群均不压缩。管理员手动 /compact 不受此限制。
      </p>
      <div class="flex items-center gap-2 flex-wrap sm:flex-nowrap">
        <select id="compact-whitelist-select" class="input input-sm text-xs flex-1 min-w-[200px]" aria-label="从最近会话选择群聊">
          <option value="">-- 从最近会话选择群聊 --</option>
          ${[...names].map(([id, name]) => `<option value="${escapeHtml(id)}">${escapeHtml(formatGroupOption(id, name))}</option>`).join('')}
        </select>
        <div class="flex items-center gap-2 w-full sm:w-auto">
          <input type="text" id="compact-whitelist-input" class="input input-sm text-xs font-mono flex-1 sm:w-44" placeholder="或手动输入群号" aria-label="手动输入群号" />
          <button type="button" id="compact-whitelist-add-btn" class="btn btn-secondary btn-sm text-xs shrink-0 flex items-center gap-1">
            ${icon('plus', 'w-3 h-3')}
            <span>添加群</span>
          </button>
        </div>
      </div>
      <div class="flex flex-col gap-1.5">
        <span class="text-[11px] font-medium text-muted">已配置白名单群：</span>
        <div id="compact-whitelist-tags" class="flex flex-wrap gap-1.5 min-h-[1.75rem] items-center p-2 rounded bg-background border border-border">
          ${groups.length === 0
            ? '<span class="text-[11px] text-muted">白名单为空，所有群均不压缩。</span>'
            : groups.map((id) => `
              <span class="inline-flex items-center gap-1.5 px-2 py-0.5 rounded bg-muted border border-border text-xs max-w-full">
                <span class="break-all">${escapeHtml(label(id))}</span>
                <button type="button" data-compact-group-id="${escapeHtml(id)}" aria-label="移除群聊 ${escapeHtml(label(id))}" class="proactive-remove-group-btn">×</button>
              </span>`).join('')}
        </div>
      </div>
    </div>`;

  const cb = parent.querySelector<HTMLInputElement>('#compact-whitelist-cb')!;
  const select = parent.querySelector<HTMLSelectElement>('#compact-whitelist-select')!;
  const input = parent.querySelector<HTMLInputElement>('#compact-whitelist-input')!;
  const addBtn = parent.querySelector<HTMLButtonElement>('#compact-whitelist-add-btn')!;

  // Always derive from the live draft: each write must see the previous one, and the card
  // is re-mounted afterwards so its tags and handlers reflect the new state.
  const currentGroups = () => parseWhitelistGroups(deps.get(COMPACT_WHITELIST_GROUPS_KEY) ?? '');
  const apply = (nextGroups: string[]) => {
    if (!deps.canEdit()) return;
    const changes = planCompactGroupChange(
      { enabledRaw: deps.get(COMPACT_WHITELIST_ENABLED_KEY), effectiveEnabled: cb.checked },
      nextGroups,
    );
    for (const { key, value } of changes) deps.change(key, value);
    mountGroupCompactWhitelist(parent, deps);
  };
  const add = () => {
    if (!input.value.trim()) return;
    apply(parseWhitelistGroups([...currentGroups(), input.value].join(',')));
  };

  cb.addEventListener('change', () => {
    if (!deps.canEdit()) return;
    deps.change(COMPACT_WHITELIST_ENABLED_KEY, cb.checked ? 'true' : 'false');
  });
  select.addEventListener('change', () => { input.value = select.value; });
  addBtn.addEventListener('click', add);
  input.addEventListener('keydown', (event) => {
    if (event.key === 'Enter' && !event.isComposing) { event.preventDefault(); add(); }
  });
  parent.querySelectorAll<HTMLButtonElement>('[data-compact-group-id]').forEach((button) => {
    button.addEventListener('click', () => {
      apply(currentGroups().filter((id) => id !== button.dataset.compactGroupId));
    });
  });
}
