import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers/promises';
import { createInstanceAPI } from '../src/api/client';
import { toast } from '../src/components/toast';
import { instanceState } from '../src/instance-state';
import { mountConnectionsPage } from '../src/pages/connections';

class Element {
  private html = '';
  private children = new Map<string, Element>();
  private listeners = new Map<string, () => void>();
  value = '';
  textContent = '';
  hidden = false;
  disabled = false;
  type = '';
  get innerHTML() { return this.html; }
  set innerHTML(html: string) {
    this.html = html;
    this.children.clear();
    for (const match of html.matchAll(/<[a-z]+\b([^>]*\bid="([^"]+)"[^>]*)>/g)) {
      const child = new Element();
      child.hidden = /\bhidden\b/.test(match[1]);
      child.disabled = /\bdisabled\b/.test(match[1]);
      child.type = match[1].match(/\btype="([^"]*)"/)?.[1] || '';
      this.children.set(match[2], child);
    }
  }
  querySelector(selector: string): Element | null {
    return this.children.get(selector.slice(1)) || null;
  }
  addEventListener(name: string, listener: () => void) { this.listeners.set(name, listener); }
  emit(name: string) { if (!this.disabled) this.listeners.get(name)?.(); }
  click() { this.emit('click'); }
}

const copies: string[] = [];
Object.assign(globalThis, {
  window: Object.assign(new EventTarget(), { location: { origin: 'http://localhost:8088' }, isSecureContext: true }),
  document: { querySelectorAll: () => [] },
});
Object.defineProperty(globalThis, 'navigator', {
  configurable: true,
  value: { clipboard: { async writeText(value: string) { copies.push(value); } } },
});

const first = { id: 'a1b2c3d4', name: 'Synthetic <instance>', enabled: true, created_at: '' };
const second = { id: 'b1c2d3e4', name: 'Other instance', enabled: true, created_at: '' };
const errors: string[] = [];
const successes: string[] = [];
toast.error = (value) => { errors.push(value); };
toast.success = (value) => { successes.push(value); };
type Setting = { key: string; value: string; isSecret: boolean };
type Change = { key: string; value: string | null; isSecret: boolean };
const prefixes = ['ONEBOT', 'ASTRBOT'];
let values: Setting[] = prefixes.flatMap((prefix) => ['MODE', 'URL', 'TOKEN'].map((field) => ({
  key: `${prefix}_WS_${field}`, value: '', isSecret: field === 'TOKEN',
})));
let failLoad = false;
let failSave = false;
let holdLoad: Promise<void> | null = null;
let holdSave: Promise<void> | null = null;
let saveSignal: AbortSignal | undefined;
const writes: Change[][] = [];
const api = {
  async listEnvVars() {
    const snapshot = values.map((value) => ({ ...value }));
    if (holdLoad) await holdLoad;
    if (failLoad) throw new Error('synthetic load failure');
    return snapshot;
  },
  async getOverview() { return { wsListenAddr: '0.0.0.0:1234' }; },
  async updateEnvVars(changes: Change[], signal: AbortSignal) {
    signal.throwIfAborted();
    saveSignal = signal;
    writes.push(changes);
    if (holdSave) await holdSave;
    if (failSave) return { success: false, error: 'synthetic save failure' };
    values = values.map((setting) => {
      const change = changes.find((candidate) => candidate.key === setting.key);
      return change ? { ...setting, value: change.value ?? '' } : setting;
    });
    return { success: true, error: '' };
  },
} as unknown as ReturnType<typeof createInstanceAPI>;

instanceState.select(first);
let root = new Element();
let cleanup = mountConnectionsPage(root as unknown as HTMLElement, api);
const el = (id: string) => {
  const found = root.querySelector(`#${id}`);
  assert.ok(found, `missing element ${id}`);
  return found;
};
const edit = (id: string, value: string) => {
  el(id).value = value;
  el(id).emit(id.endsWith('-mode') ? 'change' : 'input');
};
await setImmediate();
assert.match(root.innerHTML, /Synthetic &lt;instance&gt;/, 'instance name must be escaped');
assert.equal(el('onebot-mode').value, 'reverse', 'missing mode must preserve existing server behavior');
assert.equal(el('astrbot-mode').value, 'reverse');
assert.equal(el('onebot-forward').hidden, true);
assert.equal(el('connections-save').disabled, true);
assert.equal(el('connections-restore').disabled, true);
assert.equal(el('connections-draft').hidden, true);
assert.equal(el('onebot-address').textContent, 'ws://localhost:1234/instances/a1b2c3d4/ws/onebot');
assert.equal(el('astrbot-address').textContent, 'ws://localhost:1234/instances/a1b2c3d4/ws/astrbot');
el('onebot-copy').click();
el('astrbot-copy').click();
await setImmediate();
assert.deepEqual(copies, [el('onebot-address').textContent, el('astrbot-address').textContent]);

edit('onebot-mode', 'forward');
assert.equal(el('onebot-forward').hidden, false);
assert.equal(el('onebot-reverse').hidden, true);
assert.equal(el('onebot-copy').disabled, true, 'forward mode must not offer an inbound address');
assert.equal(el('onebot-token').type, 'password');
assert.equal(writes.length, 0, 'mode changes must stay local until saved');
el('connections-save').click();
await setImmediate();
assert.equal(writes.length, 0, 'forward mode needs an upstream URL');
assert.match(errors.at(-1)!, /OneBot.*ws:\/\//);

for (const invalid of ['https://upstream.invalid/', 'ws://name:synthetic-secret@upstream.invalid/', 'ws://upstream.invalid/#fragment', 'ws://upstream.invalid/ white']) {
  edit('onebot-url', invalid);
  el('connections-save').click();
  await setImmediate();
  assert.equal(writes.length, 0, `invalid WS URL was submitted: ${invalid}`);
  assert.ok(!errors.at(-1)!.includes('synthetic-secret'), 'URL credentials leaked into an error');
}
edit('onebot-url', 'wss://upstream.invalid/onebot');
edit('onebot-token', 'synthetic\nheader');
el('connections-save').click();
await setImmediate();
assert.equal(writes.length, 0, 'header newlines must be rejected');
edit('onebot-token', 'synthetic-onebot-token');
edit('astrbot-mode', 'forward');
edit('astrbot-url', 'ws://astrbot.invalid:6199/');
edit('astrbot-token', 'synthetic-astrbot-token');
assert.equal(el('connections-draft').hidden, false);
assert.ok(!root.innerHTML.includes('synthetic-onebot-token'), 'secret values must not be interpolated into HTML');

failSave = true;
el('connections-save').click();
await setImmediate();
assert.equal(writes.length, 1);
assert.equal(writes[0].length, 6, 'the two adapters must be saved in one atomic batch');
assert.deepEqual(writes[0].find((change) => change.key === 'ONEBOT_WS_TOKEN'), {
  key: 'ONEBOT_WS_TOKEN', value: 'synthetic-onebot-token', isSecret: true,
});
assert.equal(el('onebot-url').value, 'wss://upstream.invalid/onebot');
assert.equal(el('onebot-token').value, 'synthetic-onebot-token');
assert.equal(el('connections-draft').hidden, false, 'failed saves must retain the draft');
assert.equal(el('connections-save').disabled, false, 'failed saves must be retryable');
failSave = false;
el('connections-save').click();
await setImmediate();
assert.equal(writes.length, 2);
assert.equal(el('connections-draft').hidden, true);
assert.equal(el('connections-save').disabled, true);

edit('onebot-mode', 'reverse');
assert.equal(el('onebot-url').value, 'wss://upstream.invalid/onebot', 'reverse mode must retain the configured forward address');
assert.equal(el('onebot-token').value, 'synthetic-onebot-token');
el('connections-restore').click();
assert.equal(el('onebot-mode').value, 'forward');
assert.equal(el('connections-draft').hidden, true);
edit('onebot-mode', 'reverse');
el('connections-save').click();
await setImmediate();
assert.deepEqual(writes.at(-1), [{ key: 'ONEBOT_WS_MODE', value: 'reverse', isSecret: false }]);
assert.equal(values.find((setting) => setting.key === 'ONEBOT_WS_TOKEN')?.value, 'synthetic-onebot-token');
cleanup();
assert.equal(el('onebot-token').value, '', 'unmount should clear the token field');

root = new Element();
cleanup = mountConnectionsPage(root as unknown as HTMLElement, api);
await setImmediate();
assert.equal(el('onebot-mode').value, 'reverse');
assert.equal(el('astrbot-mode').value, 'forward');
assert.equal(el('onebot-url').value, 'wss://upstream.invalid/onebot', 'remount must retain the forward settings');
edit('astrbot-token', '');
el('connections-save').click();
await setImmediate();
assert.deepEqual(writes.at(-1), [{ key: 'ASTRBOT_WS_TOKEN', value: null, isSecret: true }], 'clearing a token must use the clearKeys API');

let releaseSave!: () => void;
holdSave = new Promise<void>((resolve) => { releaseSave = resolve; });
edit('onebot-mode', 'forward');
const successesBeforeSwitch = successes.length;
el('connections-save').click();
await setImmediate();
assert.equal(el('onebot-url').disabled, true, 'inputs must be frozen while the snapshot saves');
assert.equal(saveSignal?.aborted, false);
instanceState.select(second);
assert.equal(saveSignal?.aborted, true, 'switching instance must abort an in-flight save');
const writesBeforeSwitch = writes.length;
el('connections-save').click();
assert.equal(writes.length, writesBeforeSwitch, 'stale page must not save for a new instance');
releaseSave();
await setImmediate();
assert.equal(successes.length, successesBeforeSwitch, 'late save completion must not notify the new instance');
holdSave = null;
cleanup();

instanceState.select(first);
let releaseLoad!: () => void;
holdLoad = new Promise<void>((resolve) => { releaseLoad = resolve; });
root = new Element();
cleanup = mountConnectionsPage(root as unknown as HTMLElement, api);
instanceState.select(second);
instanceState.select(first);
releaseLoad();
await setImmediate();
assert.equal(el('onebot-address').textContent, '', 'switching away and back must invalidate the original load');
assert.equal(el('onebot-mode').disabled, true);
cleanup();
holdLoad = null;

failLoad = true;
root = new Element();
cleanup = mountConnectionsPage(root as unknown as HTMLElement, api);
await setImmediate();
assert.equal(el('connections-error').hidden, false);
assert.equal(el('connections-retry').hidden, false);
assert.equal(el('connections-save').disabled, true);
assert.equal(el('onebot-mode').disabled, true, 'failed loading must not overwrite saved settings with defaults');
failLoad = false;
el('connections-retry').click();
await setImmediate();
assert.equal(el('connections-error').hidden, true);
assert.equal(el('onebot-mode').disabled, false);
cleanup();

instanceState.select(null);
root = new Element();
cleanup = mountConnectionsPage(root as unknown as HTMLElement, api);
assert.match(root.innerHTML, /请选择实例/);
cleanup();
console.log('PASS: connection modes, copy URLs, atomic drafts, validation, token handling, retry and instance isolation');
