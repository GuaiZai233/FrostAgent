import assert from 'node:assert/strict';
import { mountBackendSettingsPage } from '../src/pages/backend-settings';
import { SettingsDraft } from '../src/pages/settings-draft';
import { createInstanceAPI, extraEnvAPI } from '../src/api/client';
import { instanceState } from '../src/instance-state';
import { toast } from '../src/components/toast';

// The page's dedicated controls use the same event harness as the persona tests.
// Numeric validity models the native required/min/max checks used during save.
class Element {
  private html = '';
  private children = new Map<string, Element>();
  private rows: Element[] = [];
  private listeners = new Map<string, (event: unknown) => void>();
  value = '';
  textContent = '';
  disabled = false;
  checked = false;
  hidden = false;
  required = false;
  placeholder = '';
  validity = { badInput: false };
  type = '';
  tag = '';
  min = '';
  max = '';
  dataset: Record<string, string> = {};
  style: Record<string, string> = {};
  set innerHTML(html: string) {
    this.html = html;
    this.children.clear();
    this.rows = [];
    for (const match of html.matchAll(/<([a-z]+)\b([^>]*\bid="([^"]+)"[^>]*)>/g)) {
      const child = new Element();
      child.tag = match[1];
      const attrs = match[2];
      const attr = (name: string) => attrs.match(new RegExp(`\\b${name}="([^"]*)"`))?.[1] ?? '';
      child.type = attr('type');
      child.value = attr('value');
      if (child.tag === 'select') {
        const options = html.slice(match.index! + match[0].length, html.indexOf('</select>', match.index));
        child.value = options.match(/<option\b[^>]*value="([^"]*)"[^>]*selected/)?.[1] ?? '';
      }
      child.placeholder = attr('placeholder');
      child.min = attr('min');
      child.max = attr('max');
      child.required = /\brequired\b/.test(attrs);
      child.disabled = /\bdisabled\b/.test(attrs);
      child.hidden = /\bhidden\b/.test(attrs);
      child.dataset.settingInput = attr('data-setting-input');
      child.dataset.default = attr('data-default');
      this.children.set(match[3], child);
    }
    const rows = [...html.matchAll(/data-setting-row="([^"]+)"/g)];
    rows.forEach((match, index) => {
      const row = new Element();
      row.dataset.settingRow = match[1];
      row.innerHTML = html.slice(match.index! + match[0].length, rows[index + 1]?.index ?? html.length);
      // Reuse these parsed inputs when the page queries numeric fields globally.
      row.children.forEach((child, id) => this.children.set(id, child));
      this.rows.push(row);
    });
  }
  get innerHTML() { return this.html; }
  querySelector(selector: string): Element | null {
    if (selector.startsWith('#')) return this.children.get(selector.slice(1)) ?? [...this.children.values()].map((child) => child.querySelector(selector)).find(Boolean) ?? null;
    if (selector === '[data-setting-input]') return this.all().find((child) => child.dataset.settingInput) ?? null;
    const key = selector.match(/data-setting-input="([^"]+)"/)?.[1];
    return key ? this.all().find((input) => input.type === 'number' && input.dataset.settingInput === key) ?? null : null;
  }
  private all(): Element[] { return [...this.children.values()].flatMap((child) => [child, ...child.all()]); }
  querySelectorAll(selector: string): Element[] {
    if (selector === '[data-setting-row]') return this.rows;
    if (selector === 'input, select, button') return this.all().filter((child) => ['input', 'select', 'button'].includes(child.tag));
    if (selector === 'input[type="number"]') return this.all().filter((child) => child.type === 'number');
    return [];
  }
  addEventListener(name: string, listener: (event: unknown) => void) { this.listeners.set(name, listener); }
  emit(name: string) { if (!this.disabled) this.listeners.get(name)?.({ currentTarget: this, preventDefault() {} }); }
  click() { this.emit('click'); }
  reportValidity() {
    if (this.disabled) return true;
    if (this.validity.badInput) return false;
    if (!this.value) return !this.required;
    const n = Number(this.value);
    return Number.isFinite(n) && (!this.min || n >= Number(this.min)) && (!this.max || n <= Number(this.max)) &&
      (this.type !== 'number' || this.dataset.settingInput !== 'BILLING_PROMPT_PRICE_PER_MILLION' || Number.isInteger(n));
  }
}

const errors: string[] = [];
toast.error = (message) => { errors.push(message); };
toast.success = () => {};
Object.assign(globalThis, { window: new EventTarget(), document: { querySelectorAll: () => [] } });
Object.assign(globalThis, { HTMLInputElement: Element, HTMLSelectElement: class {} });
instanceState.select({ id: 'a1b2c3d4', name: 'test', enabled: false, created_at: '' });
let values = [
  { key: 'MAX_CONTEXT_MESSAGES', value: '20', resetHint: '50', isSecret: false },
  { key: 'ENABLE_AT_IN_GROUP_MSG', value: 'false', isSecret: false },
  { key: 'GROUP_COMPACT_MAX_BUFFER_SIZE', value: '', resetHint: '200', isSecret: false },
  { key: 'SECURITY_GATEWAY_TIMEOUT', value: '50s', resetHint: '50s（全局继承 · 网关超时）', isInherited: true, isSecret: false },
  { key: 'BILLING_PROMPT_PRICE_PER_MILLION', value: '', isSecret: false },
  { key: 'BILLING_SAFETY_MULTIPLIER', value: '1.2', isSecret: false },
  { key: 'QUOTE_TRACE_DEPTH', value: '5', isSecret: false },
  { key: 'ENABLE_QUOTE_TRACE', value: 'true', isSecret: false },
  { key: 'PROACTIVE_REPLY_PROBABILITY', value: '0.05', isSecret: false },
  { key: 'ENABLE_PROACTIVE_REPLY', value: 'true', isSecret: false },
  { key: 'BOT_LOOP_MAX_TURNS', value: '3', isSecret: false },
  { key: 'STICKER_ACTIVITY', value: '5', isSecret: false },
  { key: 'SECURITY_CONTROL_MODE', value: 'aggressive', resetHint: '基础模式', isSecret: false },
  { key: 'BOT_LOOP_MODE', value: 'hard', isSecret: false },
  { key: 'STICKER_ACTIVITY_MODE', value: 'all_messages', isSecret: false },
];
let fail = false;
let failLoad = true;
let holdExtra = false;
let releaseExtra: (() => void) | undefined;
let pageSignal: AbortSignal | undefined;
const writes: Array<Array<{ key: string; value: string | null; isSecret: boolean }>> = [];
const extraWrites: string[] = [];
const extras = new Map<string, string>();
const api = {
  async listEnvVars() { if (failLoad) throw new Error('synthetic initial load failure'); return values; },
  async getSessions() { return { sessions: [] }; },
  async updateEnvVars(changes: typeof writes[number], signal: AbortSignal) {
    signal.throwIfAborted();
    pageSignal = signal;
    writes.push(changes);
    if (fail) return { success: false, error: 'synthetic save failure' };
    values = values.map((item) => {
      const change = changes.find(({ key }) => key === item.key);
      return change ? { ...item, value: change.value ?? '', isInherited: false } : item;
    });
    return { success: true, error: '' };
  },
} as unknown as ReturnType<typeof createInstanceAPI>;
const extraAPI = {
  async list() { return [...extras].map(([key, value]) => ({ key, value })); },
  async put({ key, value }: { key: string; value: string }, signal: AbortSignal) {
    signal.throwIfAborted();
    pageSignal = signal;
    extraWrites.push(key);
    if (holdExtra) await new Promise<void>((resolve) => { releaseExtra = resolve; });
    extras.set(key, value);
  },
  async delete(key: string) { extras.delete(key); },
} as typeof extraEnvAPI;
const settle = () => new Promise<void>((resolve) => setTimeout(resolve, 0));
const root = new Element();
let cleanup = mountBackendSettingsPage(root as unknown as HTMLElement, api, extraAPI);
const el = (id: string) => root.querySelector(`#${id}`)!;
await settle();
assert.equal(el('backend-refresh-btn').disabled, false, 'initial load failures must allow retry');
assert.equal(el('group-at-cb').disabled, true);
failLoad = false;
el('backend-refresh-btn').click();
await settle();
assert.equal(el('group-at-cb').disabled, false);
assert.equal(el('settings-draft-banner').hidden, true);
assert.equal(el('setting-GROUP_COMPACT_MAX_BUFFER_SIZE').value, '');
assert.equal(el('setting-GROUP_COMPACT_MAX_BUFFER_SIZE').placeholder, '200');
assert.equal(el('setting-SECURITY_GATEWAY_TIMEOUT').value, '', 'inherited values appear only as placeholders');
assert.match(el('setting-SECURITY_GATEWAY_TIMEOUT').placeholder, /50s.*全局继承/);

for (const [key, invalid] of [['BILLING_PROMPT_PRICE_PER_MILLION', '1.25'], ['BILLING_SAFETY_MULTIPLIER', '0']]) {
  const input = el(`setting-${key}`);
  input.value = invalid;
  input.emit('input');
  const before = writes.length;
  el('settings-draft-save').click();
  await settle();
  assert.equal(writes.length, before, `${key} must reject values the runtime would ignore`);
  el('settings-draft-restore').click();
}
const toggle = el('group-at-cb');
toggle.checked = true;
toggle.emit('change');
assert.equal(writes.length, 0, 'changing a switch must only stage a draft');
assert.equal(el('settings-draft-banner').hidden, false);
toggle.checked = false;
toggle.emit('change');
assert.equal(el('settings-draft-banner').hidden, true, 'reverting a value removes the dirty state');
toggle.checked = true;
toggle.emit('change');
el('settings-draft-restore').click();
assert.equal(toggle.checked, false);
assert.equal(writes.length, 0, 'restore must not write to the server');
toggle.checked = true;
toggle.emit('change');
el('settings-draft-save').click();
await settle();
assert.equal(writes.length, 1, 'empty optional numeric fields must not block a new instance switch save');
assert.deepEqual(writes[0].map(({ key, value }) => ({ key, value })), [{ key: 'ENABLE_AT_IN_GROUP_MSG', value: 'true' }]);
assert.equal(el('settings-draft-banner').hidden, true);

fail = true;
toggle.checked = false;
toggle.emit('change');
el('settings-draft-save').click();
await settle();
assert.equal(el('settings-draft-banner').hidden, false, 'failed saves retain the draft');
assert.equal(toggle.checked, false);
assert.match(errors.at(-1)!, /synthetic save failure/);
fail = false;
el('settings-draft-save').click();
await settle();
assert.equal(el('settings-draft-banner').hidden, true);

for (const [numberId, toggleId] of [
  ['quote-trace-depth', 'quote-trace-cb'], ['proactive-reply-number', 'proactive-reply-cb'],
]) {
  el(numberId).value = '0';
  el(numberId).emit('input');
  el(toggleId).checked = false;
  el(toggleId).emit('change');
  assert.equal(el(numberId).disabled, true);
  const before = writes.length;
  el('settings-draft-save').click();
  await settle();
  assert.equal(writes.length, before, 'disabled controls must not hide an invalid pending number');
  el('settings-draft-restore').click();
}

const context = el('setting-MAX_CONTEXT_MESSAGES');
context.value = '';
context.emit('input');
assert.equal(el('settings-draft-banner').hidden, false);
assert.equal(context.disabled, false, 'empty default inputs must remain editable');
el('settings-draft-restore').click();
assert.equal(el('setting-MAX_CONTEXT_MESSAGES').value, '20');
el('setting-MAX_CONTEXT_MESSAGES').value = '';
el('setting-MAX_CONTEXT_MESSAGES').emit('input');
fail = true;
el('settings-draft-save').click();
await settle();
assert.equal(writes.at(-1)?.find(({ key }) => key === 'MAX_CONTEXT_MESSAGES')?.value, null, 'empty numeric input must clear its override');
assert.equal(el('setting-MAX_CONTEXT_MESSAGES').value, '', 'failed saves retain the empty draft');
assert.equal(el('settings-draft-banner').hidden, false);
fail = false;
el('settings-draft-save').click();
await settle();
assert.equal(el('setting-MAX_CONTEXT_MESSAGES').value, '');
assert.equal(el('setting-MAX_CONTEXT_MESSAGES').placeholder, '50');
assert.equal(el('settings-draft-banner').hidden, true);

for (const [id, key, fallback] of [
  ['quote-trace-depth', 'QUOTE_TRACE_DEPTH', '5'],
  ['proactive-reply-number', 'PROACTIVE_REPLY_PROBABILITY', '0.01'],
  ['bot-loop-max-turns', 'BOT_LOOP_MAX_TURNS', '3'],
  ['sticker-activity-number', 'STICKER_ACTIVITY', '0'],
]) {
  el(id).value = '';
  el(id).emit('input');
  el('settings-draft-save').click();
  await settle();
  assert.equal(writes.at(-1)?.find((change) => change.key === key)?.value, null);
  assert.equal(el(id).value, '');
  assert.equal(el(id).placeholder, fallback);
}
assert.equal(el('proactive-reply-cb').checked, true);
assert.equal(el('proactive-reply-slider').value, '0.01', 'empty enabled probability uses the actual runtime default');
for (const [id, key] of [
  ['setting-SECURITY_CONTROL_MODE', 'SECURITY_CONTROL_MODE'],
  ['bot-loop-mode', 'BOT_LOOP_MODE'], ['sticker-activity-mode', 'STICKER_ACTIVITY_MODE'],
]) {
  el(id).value = '';
  el(id).emit('change');
  el('settings-draft-save').click();
  await settle();
  assert.equal(writes.at(-1)?.find((change) => change.key === key)?.value, null);
  assert.equal(el(id).value, '');
  assert.equal(el(id).dataset.default, 'true');
}

const malformed = el('setting-MAX_CONTEXT_MESSAGES');
malformed.value = '';
malformed.validity.badInput = true;
malformed.emit('input');
assert.equal(el('settings-draft-banner').hidden, false, 'malformed numeric text remains a draft even when the browser value is empty');
const beforeMalformed = writes.length;
el('settings-draft-save').click();
await settle();
assert.equal(writes.length, beforeMalformed, 'malformed numeric text must not be saved as a default');
el('settings-draft-restore').click();

el('extra-env-key').value = 'SYNTHETIC_TOKEN';
el('extra-env-value').value = 'example';
el('extra-env-add').click();
assert.equal(extraWrites.length, 0, 'adding extra env must only stage a draft');
el('settings-draft-restore').click();
assert.equal(extraWrites.length, 0);
el('extra-env-key').value = 'SYNTHETIC_TOKEN';
el('extra-env-value').value = 'example';
toggle.checked = true;
toggle.emit('change');
holdExtra = true;
const beforeLeaving = writes.length;
el('settings-draft-save').click();
await settle();
assert.equal(extraWrites.length, 1, 'top save also includes an unfinished extra env composer');
cleanup();
assert.equal(pageSignal?.aborted, true);
releaseExtra?.();
await settle();
assert.equal(writes.length, beforeLeaving, 'leaving stops later requests in the save sequence');
cleanup = mountBackendSettingsPage(root as unknown as HTMLElement, api, extraAPI);
await settle();
assert.equal(el('settings-draft-banner').hidden, true, 'returning does not recover discarded page drafts');
assert.equal(el('group-at-cb').checked, false);
cleanup();

const draft = new SettingsDraft([{ key: 'BOT_NAME', value: 'old', isSecret: false }], [{ key: 'EMPTY', value: '' }]);
draft.set('BOT_NAME', '');
assert.equal(draft.pending()[0].value, null);
draft.set('BOT_NAME', 'old');
assert.equal(draft.dirty, false);
draft.deleteExtra('EMPTY');
assert.deepEqual(draft.extraChanges(), [{ key: 'EMPTY', value: null }], 'deleting an empty env value is still a change');
draft.acknowledgeExtra('EMPTY', null);
draft.set('BOT_NAME', 'new');
draft.restore();
assert.equal(draft.get('BOT_NAME'), 'old');
assert.deepEqual(draft.extraEntries(), [], 'restore keeps already successful extra writes');
const inherited = new SettingsDraft([{ key: 'SECURITY_GATEWAY_TIMEOUT', value: '50s', isSecret: false, isInherited: true }], []);
assert.equal(inherited.get('SECURITY_GATEWAY_TIMEOUT'), '');
inherited.set('SECURITY_GATEWAY_TIMEOUT', '35s');
inherited.set('SECURITY_GATEWAY_TIMEOUT', '');
assert.equal(inherited.dirty, false, 'clearing an unsaved inheritance override returns to the saved default');
process.stdout.write('PASS: server settings draft, restore, failures, numeric validation and navigation cancellation\n');
