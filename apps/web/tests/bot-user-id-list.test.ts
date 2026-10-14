import assert from 'node:assert/strict';
import { mountBackendSettingsPage } from '../src/pages/backend-settings';
import { createInstanceAPI } from '../src/api/client';
import { toast } from '../src/components/toast';
import { instanceState } from '../src/instance-state';

class MockElement {
  innerHTML = '';
  value = '';
  disabled = false;
  checked = false;
  hidden = false;
  textContent = '';
  className = '';
  style: Record<string, string> = {};
  private children = new Map<string, MockElement>();
  private listeners = new Map<string, (event: Event) => unknown>();
  private buttonsHTML = '';
  private buttons: MockElement[] = [];
  constructor(readonly dataset: Record<string, string> = {}) {}
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
  querySelector(selector: string): MockElement {
    if (!this.children.has(selector)) this.children.set(selector, new MockElement());
    return this.children.get(selector)!;
  }
  querySelectorAll(selector: string): MockElement[] {
    if (selector !== '[data-bot-user-index]') return [];
    if (this.buttonsHTML !== this.innerHTML) {
      this.buttonsHTML = this.innerHTML;
      this.buttons = [...this.innerHTML.matchAll(/data-bot-user-index="(\d+)"[^>]*>/g)].map((match) => {
        const button = new MockElement({ botUserIndex: match[1] });
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
const writes: Array<{ key: string; value: string; isSecret: boolean }> = [];
let releaseWrite: (() => void) | undefined;
let blockWrite = false;
let failKey = '';
let storageBackend = 'sqlite';
let storageReads = 0;
const readEnvVars = () => [...values].map(([key, value]) => ({ key, value, isSecret: false }));
const api = {
  async listEnvVars() {
    return readEnvVars();
  },
  async listEnvVarsWithStorage() {
    storageReads++;
    return { envVars: readEnvVars(), storageBackend };
  },
  async getSessions() { return { sessions: [] }; },
  async updateEnvVar(params: { key: string; value: string; isSecret: boolean }) {
    writes.push(params);
    if (blockWrite) {
      blockWrite = false;
      await new Promise<void>((resolve) => { releaseWrite = resolve; });
    }
    if (params.key === failKey) return { success: false, error: 'fixture save failure' };
    values.set(params.key, params.value);
    return { success: true };
  },
} as unknown as ReturnType<typeof createInstanceAPI>;
const settle = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
const root = new MockElement();
const cleanup = mountBackendSettingsPage(root as unknown as HTMLElement, api);
const hasEmbeddingSettings = root.innerHTML.includes('id="embedding-enabled"');
// Require the UI once the storage-aware API ships, even if the settings markup was lost.
if ('listEnvVarsWithStorage' in createInstanceAPI() || process.env.FROSTAGENT_TEST_EMBEDDING_SETTINGS === 'true') {
  assert.ok(hasEmbeddingSettings, 'the combined page must retain the Embedding settings');
}
const input = root.querySelector('#bot-loop-user-id-input');
const add = root.querySelector('#bot-loop-add-user');
const save = root.querySelector('#bot-loop-save');
const tags = root.querySelector('#bot-loop-user-tags');
const maxTurns = root.querySelector('#bot-loop-max-turns');
const mode = root.querySelector('#bot-loop-mode');
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
  assert.equal(root.querySelector('#embedding-enabled').checked, true);
  assert.equal(root.querySelector('#embedding-enabled').disabled, false);
  assert.equal(root.querySelector('#embedding-sqlite-warning').hidden, false);
}

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
type('bot-pending');
maxTurns.value = '4';
maxTurns.fire('input');
mode.value = 'hard';
mode.fire('change');
root.querySelector('#backend-refresh-btn').click();
await settle();
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
assert.deepEqual(writes, [
  { key: 'BOT_LOOP_MAX_TURNS', value: '4', isSecret: false },
  { key: 'BOT_LOOP_MODE', value: 'hard', isSecret: false },
  { key: 'BOT_LOOP_USER_IDS', value: 'telegram:CaseID,bot-b,bot-c,bot-d,bot-pending', isSecret: false },
]);
assert.equal(removeButtons().length, 5, 'saved IDs must reload into the list');
assert.equal(save.disabled, false);
assert.equal(values.get('ENABLE_EMBEDDING'), 'true', 'Bot list saves must preserve Embedding configuration');

type('bot-failed');
add.click();
failKey = 'BOT_LOOP_USER_IDS';
save.click();
await settle();
assert.match(errors.at(-1)!, /保存未完成/);
assert.doesNotMatch(tags.innerHTML, /bot-failed/, 'reload the actual configuration after partial save');
assert.equal(save.disabled, false);
failKey = '';
while (removeButtons().length) removeButtons()[0].click();
save.click();
await settle();
assert.equal(values.get('BOT_LOOP_USER_IDS'), '', 'removing all IDs must persist an empty list');
assert.equal(removeButtons().length, 0);
assert.match(tags.innerHTML, /尚未添加用户 ID/);
if (hasEmbeddingSettings) {
  const embeddingToggle = root.querySelector('#embedding-enabled');
  const embeddingWarning = root.querySelector('#embedding-sqlite-warning');
  assert.equal(embeddingToggle.checked, true, 'Bot list saves must retain the enabled switch');
  assert.equal(embeddingWarning.hidden, false, 'Bot list saves must retain the SQLite warning');
  embeddingToggle.checked = false;
  await embeddingToggle.fire('change');
  assert.equal(values.get('ENABLE_EMBEDDING'), 'false');
  assert.equal(embeddingWarning.hidden, true);
  assert.equal(removeButtons().length, 0, 'Embedding changes must preserve the Bot ID list');
  embeddingToggle.checked = true;
  await embeddingToggle.fire('change');
  assert.equal(values.get('ENABLE_EMBEDDING'), 'true');
  assert.equal(embeddingWarning.hidden, false);
  storageBackend = 'postgres';
  root.querySelector('#backend-refresh-btn').click();
  await settle();
  assert.equal(embeddingToggle.checked, true);
  assert.equal(embeddingWarning.hidden, true, 'PostgreSQL must not display the SQLite warning');
  assert.equal(add.disabled, false, 'storage-aware reload must leave the Bot ID editor usable');
}
cleanup();
