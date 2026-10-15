import assert from 'node:assert/strict';
import { mountBackendSettingsPage } from '../src/pages/backend-settings';
import { createInstanceAPI, extraEnvAPI } from '../src/api/client';
import type { SettingChange } from '../src/pages/settings-draft';
import { toast } from '../src/components/toast';
import { instanceState } from '../src/instance-state';

class MockElement {
  private html = '';
  value = '';
  disabled = false;
  checked = false;
  hidden = false;
  textContent = '';
  className = '';
  type = '';
  tag = '';
  min = '';
  max = '';
  style: Record<string, string> = {};
  private children = new Map<string, MockElement>();
  private listeners = new Map<string, (event: Event) => unknown>();
  private buttonsHTML = '';
  private buttons: MockElement[] = [];
  constructor(readonly dataset: Record<string, string> = {}) {}
  set innerHTML(html: string) {
    this.html = html;
    this.children.clear();
    for (const match of html.matchAll(/<([a-z]+)\b([^>]*\bid="([^"]+)"[^>]*)>/g)) {
      const child = new MockElement();
      const attr = (name: string) => match[2].match(new RegExp(`\\b${name}="([^"]*)"`))?.[1] ?? '';
      child.tag = match[1];
      child.type = attr('type');
      child.value = attr('value');
      child.min = attr('min');
      child.max = attr('max');
      child.disabled = /\bdisabled\b/.test(match[2]);
      this.children.set('#' + match[3], child);
    }
  }
  get innerHTML() { return this.html; }
  addEventListener(type: string, callback: (event: Event) => unknown) {
    this.listeners.set(type, callback);
  }
  fire(type: string, event = new Event(type)) {
    return this.listeners.get(type)?.(event);
  }
  click() { if (!this.disabled) return this.fire('click'); }
  keydown(key: string, isComposing = false) {
    const event = Object.assign(new Event('keydown', { cancelable: true }), { key, isComposing });
    if (!this.disabled) this.fire('keydown', event);
    return event;
  }
  focus() {}
  reportValidity() {
    return this.value === '' || (Number.isSafeInteger(Number(this.value)) &&
      (!this.min || Number(this.value) >= Number(this.min)) && (!this.max || Number(this.value) <= Number(this.max)));
  }
  querySelector(selector: string): MockElement | null {
    return this.children.get(selector) ?? null;
  }
  private all(): MockElement[] {
    return [...this.children.values()].flatMap((child) => [child, ...child.all()])
      .concat(this.querySelectorAll('[data-bot-user-index]'));
  }
  querySelectorAll(selector: string): MockElement[] {
    if (selector === 'input, select, button') return this.all().filter((child) => ['input', 'select', 'button'].includes(child.tag));
    if (selector === 'input[type="number"]') return this.all().filter((child) => child.type === 'number');
    if (selector !== '[data-bot-user-index]') return [];
    if (this.buttonsHTML !== this.innerHTML) {
      this.buttonsHTML = this.innerHTML;
      this.buttons = [...this.innerHTML.matchAll(/data-bot-user-index="(\d+)"[^>]*>/g)].map((match) => {
        const button = new MockElement({ botUserIndex: match[1] });
        button.tag = 'button';
        button.disabled = match[0].includes('disabled');
        return button;
      });
    }
    return this.buttons;
  }
}

const errors: string[] = [];
Object.assign(globalThis, {
  window: Object.assign(new EventTarget(), { location: { origin: 'http://localhost' } }),
  document: { querySelectorAll: () => [] },
});
instanceState.select({ id: 'fixture-instance', name: 'fixture', enabled: false, created_at: '' });
toast.error = (message) => { errors.push(message); };
toast.success = () => {};
const values = new Map([
  ['BOT_LOOP_USER_IDS', 'bot-a; Telegram:CaseID\nbot-a,bot-b'],
  ['BOT_LOOP_MAX_TURNS', '3'],
  ['BOT_LOOP_MODE', 'soft'],
  ['ENABLE_EMBEDDING', 'true'],
]);
const writes: SettingChange[][] = [];
let releaseWrite: (() => void) | undefined;
let blockWrite = false;
let failWrite = false;
let storageBackend = 'sqlite';
let storageReads = 0;
let reads = 0;
const readEnvVars = () => [...values].map(([key, value]) => ({ key, value, isSecret: false }));
const api = {
  async listEnvVars() {
    reads++;
    return readEnvVars();
  },
  async listEnvVarsWithStorage() {
    reads++;
    storageReads++;
    return { envVars: readEnvVars(), storageBackend };
  },
  async getSessions() { return { sessions: [] }; },
  async updateEnvVars(changes: SettingChange[], signal: AbortSignal) {
    signal.throwIfAborted();
    writes.push(changes);
    if (blockWrite) {
      blockWrite = false;
      await new Promise<void>((resolve) => { releaseWrite = resolve; });
    }
    if (failWrite) return { success: false, error: 'fixture save failure' };
    changes.forEach(({ key, value }) => values.set(key, value ?? ''));
    return { success: true };
  },
} as unknown as ReturnType<typeof createInstanceAPI>;
const settle = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
const root = new MockElement();
const extraAPI = { async list() { return []; } } as typeof extraEnvAPI;
const cleanup = mountBackendSettingsPage(root as unknown as HTMLElement, api, extraAPI);
const el = (id: string) => root.querySelector('#' + id)!;
const hasEmbeddingSettings = root.innerHTML.includes('id="embedding-enabled"');
// Require the UI once the storage-aware API ships, even if the settings markup was lost.
if ('listEnvVarsWithStorage' in createInstanceAPI() || process.env.FROSTAGENT_TEST_EMBEDDING_SETTINGS === 'true') {
  assert.ok(hasEmbeddingSettings, 'the combined page must retain the Embedding settings');
}
const input = el('bot-loop-user-id-input');
const add = el('bot-loop-add-user');
const save = el('settings-draft-save');
const restore = el('settings-draft-restore');
const banner = el('settings-draft-banner');
const tags = el('bot-loop-user-tags');
const maxTurns = el('bot-loop-max-turns');
const mode = el('bot-loop-mode');
const removeButtons = () => tags.querySelectorAll('[data-bot-user-index]');
const type = (value: string) => { input.value = value; input.fire('input'); };
assert.equal(add.disabled, true, 'editing must wait for the existing configuration');
await settle();
assert.equal(add.disabled, false);
assert.equal(input.value, '', 'existing IDs belong in the list, not the add input');
assert.equal(removeButtons().length, 3, 'load legacy separators and remove duplicates');
assert.match(tags.innerHTML, /telegram:CaseID/);
if (hasEmbeddingSettings) {
  assert.equal(storageReads, 1, 'load configuration through the storage-aware API');
  assert.equal(el('embedding-enabled').checked, true);
  assert.equal(el('embedding-enabled').disabled, false);
  assert.equal(el('embedding-sqlite-warning').hidden, false);
}

assert.equal(banner.hidden, true, 'rendering existing IDs must not create a draft');
type('bot-to-restore');
add.click();
assert.equal(banner.hidden, false);
assert.equal(writes.length, 0, 'adding IDs must only update the draft');
restore.click();
assert.equal(removeButtons().length, 3);
assert.equal(banner.hidden, true);
type('bot-unadded');
assert.equal(banner.hidden, false, 'pending input must participate in the unsaved-changes banner');
assert.equal(el('backend-refresh-btn').disabled, true);
restore.click();
assert.equal(input.value, '');
assert.equal(banner.hidden, true);
assert.equal(writes.length, 0, 'restoring a list must not write to the server');
type(' bot-c ');
add.click();
assert.equal(input.value, '');
assert.equal(removeButtons().length, 4);
type('bot-d');
assert.equal(input.keydown('Enter').defaultPrevented, true);
assert.equal(removeButtons().length, 5);
type('bot-ime');
input.keydown('Enter', true);
assert.equal(removeButtons().length, 5, 'IME confirmation must not add an ID');
type('TELEGRAM:CaseID');
add.click();
assert.equal(errors.at(-1), '该用户 ID 已添加');
assert.equal(removeButtons().length, 5);
type('');
add.click();
assert.equal(errors.at(-1), '请输入用户 ID');
type('   ');
save.click();
assert.equal(errors.at(-1), '请输入用户 ID');
assert.equal(writes.length, 0, 'whitespace-only pending input must not be silently saved');
type('bot-x,bot-y');
add.click();
assert.equal(errors.at(-1), '每次添加一个用户 ID');
save.click();
assert.equal(writes.length, 0, 'invalid pending input must block saving');

type('<bot-"&>');
add.click();
assert.match(tags.innerHTML, /&lt;bot-&quot;&amp;&gt;/, 'escape IDs in text and attributes');
removeButtons().at(-1)!.click();
removeButtons()[0].click();
assert.doesNotMatch(tags.innerHTML, />bot-a</);
assert.equal(values.get('BOT_LOOP_USER_IDS'), 'bot-a; Telegram:CaseID\nbot-a,bot-b', 'removing IDs must only update the draft');
maxTurns.value = '0';
maxTurns.fire('input');
save.click();
await settle();
assert.equal(writes.length, 0, 'batch saves must preserve numeric validation');
type('bot-pending');
maxTurns.value = '4';
maxTurns.fire('input');
mode.value = 'hard';
mode.fire('change');
const readsBeforeRefresh = reads;
el('backend-refresh-btn').fire('click');
await settle();
assert.equal(reads, readsBeforeRefresh, 'refresh must not overwrite unsaved edits');
assert.equal(removeButtons().length, 4, 'unrelated reloads must retain list edits');
assert.equal(input.value, 'bot-pending');
assert.equal(maxTurns.value, '4');
assert.equal(mode.value, 'hard');

blockWrite = true;
const oldRemove = removeButtons()[0];
save.click();
assert.equal(input.value, '', 'saving includes a pending ID');
assert.equal(save.disabled, true);
assert.equal(add.disabled, true);
assert.equal(input.disabled, true);
assert.equal(maxTurns.disabled, true);
assert.equal(mode.disabled, true);
assert.ok(removeButtons().every((button) => button.disabled));
oldRemove.fire('click');
add.fire('click');
save.fire('click');
assert.equal(writes.length, 1, 'a repeated save must not issue another request');
releaseWrite!();
await settle();
assert.deepEqual([...writes[0]].sort((a, b) => a.key.localeCompare(b.key)), [
  { key: 'BOT_LOOP_MAX_TURNS', value: '4', isSecret: false },
  { key: 'BOT_LOOP_MODE', value: 'hard', isSecret: false },
  { key: 'BOT_LOOP_USER_IDS', value: 'telegram:CaseID,bot-b,bot-c,bot-d,bot-pending', isSecret: false },
], 'list, limit and mode must use one atomic batch');
assert.equal(removeButtons().length, 5, 'saved IDs must reload into the list');
assert.equal(save.disabled, false);
assert.equal(banner.hidden, true);
assert.equal(values.get('ENABLE_EMBEDDING'), 'true', 'Bot list saves must preserve Embedding configuration');

type('bot-failed');
add.click();
failWrite = true;
save.click();
await settle();
assert.match(errors.at(-1)!, /保存未完成/);
assert.match(tags.innerHTML, /bot-failed/, 'failed batch saves retain the pending list for retry');
assert.doesNotMatch(values.get('BOT_LOOP_USER_IDS')!, /bot-failed/);
assert.equal(banner.hidden, false);
assert.equal(save.disabled, false);
failWrite = false;
save.click();
await settle();
assert.match(values.get('BOT_LOOP_USER_IDS')!, /bot-failed/);
assert.equal(banner.hidden, true);
while (removeButtons().length) removeButtons()[0].click();
save.click();
await settle();
assert.equal(values.get('BOT_LOOP_USER_IDS'), '', 'removing all IDs must persist an empty list');
assert.deepEqual(writes.at(-1), [{ key: 'BOT_LOOP_USER_IDS', value: null, isSecret: false }]);
assert.equal(removeButtons().length, 0);
assert.match(tags.innerHTML, /尚未添加用户 ID/);
if (hasEmbeddingSettings) {
  const embeddingToggle = el('embedding-enabled');
  const embeddingWarning = el('embedding-sqlite-warning');
  assert.equal(embeddingToggle.checked, true, 'Bot list saves must retain the enabled switch');
  assert.equal(embeddingWarning.hidden, false, 'Bot list saves must retain the SQLite warning');
  embeddingToggle.checked = false;
  await embeddingToggle.fire('change');
  save.click();
  await settle();
  assert.equal(values.get('ENABLE_EMBEDDING'), 'false');
  assert.equal(embeddingWarning.hidden, true);
  assert.equal(removeButtons().length, 0, 'Embedding changes must preserve the Bot ID list');
  embeddingToggle.checked = true;
  await embeddingToggle.fire('change');
  save.click();
  await settle();
  assert.equal(values.get('ENABLE_EMBEDDING'), 'true');
  assert.equal(embeddingWarning.hidden, false);
  storageBackend = 'postgres';
  el('backend-refresh-btn').click();
  await settle();
  assert.equal(embeddingToggle.checked, true);
  assert.equal(embeddingWarning.hidden, true, 'PostgreSQL must not display the SQLite warning');
  assert.equal(add.disabled, false, 'storage-aware reload must leave the Bot ID editor usable');
}
cleanup();
