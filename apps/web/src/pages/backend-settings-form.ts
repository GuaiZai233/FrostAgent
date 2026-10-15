import type { SettingValue } from './settings-draft';
import { escapeHtml } from '../utils/formatters';
import { dedicatedSettingKeys, settingFields } from './backend-setting-fields';

const groupOrder = [
  '对话与上下文', '记忆与压缩', '模型与智能体', '适配器',
  '网络与连接', '安全与权限', '计费', '沙箱', 'ActionsCat',
];

export function renderBackendSettingsForm(
  root: HTMLElement,
  values: SettingValue[],
  isGlobal: (key: string) => boolean,
  change: (key: string, value: string) => void,
): void {
  const groups = new Map<string, SettingValue[]>();
  for (const value of values) {
    if (dedicatedSettingKeys.has(value.key)) continue;
    const field = settingFields[value.key];
    if (!field) continue;
    const items = groups.get(field.group) ?? [];
    items.push(value);
    groups.set(field.group, items);
  }
  root.innerHTML = groupOrder
    .filter((group) => groups.has(group))
    .map((group) => `
      <article class="card p-4">
        <h2 class="card-title text-sm font-semibold border-b border-border pb-3 mb-3">${escapeHtml(group)}</h2>
        <div class="grid grid-cols-1 lg:grid-cols-2 gap-x-6 gap-y-4">
          ${groups.get(group)!.map((item) => {
            const field = settingFields[item.key];
            const label = escapeHtml(field.label);
            const key = escapeHtml(item.key);
            const status = field.hot ? '保存后生效' : '保存时重载实例';
            const kind = field.type ?? 'text';
            const input = kind === 'switch'
              ? `<label class="toggle-switch" title="${label}"><input id="setting-${key}" type="checkbox" role="switch" aria-label="${label}" data-setting-switch="${key}" ${item.value === 'true' ? 'checked' : ''} /><span aria-hidden="true"></span></label>`
              : kind === 'select'
                ? `<div class="flex items-center gap-2"><select id="setting-${key}" class="input input-sm settings-default-select flex-1" data-default="${!item.value}" data-setting-input="${key}" aria-label="${label}">
                    <option value="" ${!item.value ? 'selected' : ''}>默认（${escapeHtml(item.resetHint || '')}）</option>
                    ${field.options?.map((option) => `<option value="${escapeHtml(option.value)}" ${item.value === option.value ? 'selected' : ''}>${escapeHtml(option.label)}</option>`).join('') ?? ''}
                  </select></div>`
              : `<div class="flex items-center gap-2 min-w-0">
                   <input id="setting-${key}" class="input input-sm settings-default-input flex-1 min-w-0" data-setting-input="${key}" aria-label="${label}"
                     type="${kind === 'secret' ? 'password' : kind === 'number' ? 'number' : 'text'}"
                     value="${escapeHtml(item.value)}"
                     placeholder="${escapeHtml(item.resetHint || '默认')}"
                     ${field.min === undefined ? '' : `min="${field.min}"`}
                     ${field.max === undefined ? '' : `max="${field.max}"`}
                     ${field.step === undefined ? '' : `step="${field.step}"`} />
                   ${kind === 'secret' ? `<button type="button" class="btn btn-outline btn-sm" data-setting-reveal="${key}" aria-label="显示或隐藏${label}">显示</button>` : ''}
                 </div>`;
            return `<div class="min-w-0" data-setting-row="${key}">
              <div class="flex items-center justify-between gap-2 mb-1.5">
                <label class="text-xs font-semibold text-foreground" for="setting-${key}">${label}</label>
                <span class="text-[11px] text-muted shrink-0">${isGlobal(item.key) ? '全局共享' : '当前实例'} · ${status}</span>
              </div>
              ${input}
              ${field.hint ? `<p class="text-[11px] text-muted mt-1">${escapeHtml(field.hint)}</p>` : ''}
            </div>`;
          }).join('')}
        </div>
      </article>`)
    .join('');

  for (const item of values) {
    const field = settingFields[item.key];
    if (!field || dedicatedSettingKeys.has(item.key)) continue;
    const row = [...root.querySelectorAll<HTMLElement>('[data-setting-row]')]
      .find((element) => element.dataset.settingRow === item.key);
    if (!row) continue;
    const input = row.querySelector<HTMLInputElement | HTMLSelectElement>('[data-setting-input]');
    const toggle = row.querySelector<HTMLInputElement>('[data-setting-switch]');
    toggle?.addEventListener('change', () => change(item.key, toggle.checked ? 'true' : 'false'));
    input?.addEventListener(field.type === 'select' ? 'change' : 'input', () => {
      if (field.type === 'select') input.dataset.default = String(!input.value);
      change(item.key, input.value);
    });
    row.querySelector<HTMLButtonElement>('[data-setting-reveal]')?.addEventListener('click', (event) => {
      if (!(input instanceof HTMLInputElement)) return;
      const button = event.currentTarget as HTMLButtonElement;
      input.type = input.type === 'password' ? 'text' : 'password';
      button.textContent = input.type === 'password' ? '显示' : '隐藏';
    });
  }
}
