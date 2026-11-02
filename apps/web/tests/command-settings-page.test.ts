import assert from 'node:assert/strict';
import { mountCommandSettingsPage } from '../src/pages/command-settings';
import { createInstanceAPI } from '../src/api/client';
import { instanceState } from '../src/instance-state';
import { toast } from '../src/components/toast';
import { dedicatedSettingKeys } from '../src/pages/backend-setting-fields';

class Element {
  private html = '';
  private children = new Map<string, Element>();
  private listeners = new Map<string, () => void>();
  value = '';
  checked = false;
  disabled = false;
  hidden = false;
  textContent = '';

  set innerHTML(html: string) {
    this.html = html;
    this.children.clear();
    for (const match of html.matchAll(/<[a-z]+\b([^>]*\bid="([^"]+)"[^>]*)>/g)) {
      const child = new Element();
      child.disabled = /\bdisabled\b/.test(match[1]);
      child.hidden = /\bhidden\b/.test(match[1]);
      child.checked = /\bchecked\b/.test(match[1]);
      this.children.set(match[2], child);
    }
  }
  get innerHTML() { return this.html; }
  querySelector(selector: string): Element | null { return this.children.get(selector.slice(1)) ?? null; }
  addEventListener(name: string, listener: () => void) { this.listeners.set(name, listener); }
  emit(name: string) { if (!this.disabled) this.listeners.get(name)?.(); }
  click() { this.emit('click'); }
  toggle() { if (!this.disabled) { this.checked = !this.checked; this.emit('change'); } }
}

type Values = Array<{ key: string; value: string; isSecret: boolean }>;
type Changes = Array<{ key: string; value: string | null; isSecret: boolean }>;
const statusKey = 'ENABLE_RUNTIME_STATUS_COMMAND';
const errors: string[] = [];
const successes: string[] = [];
toast.error = (message) => { errors.push(message); };
toast.success = (message) => { successes.push(message); };
Object.assign(globalThis, {
  window: Object.assign(new EventTarget(), { location: { origin: 'http://localhost' } }),
  document: { querySelectorAll: () => [] },
});
const settle = () => new Promise<void>((resolve) => setTimeout(resolve, 0));
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((ok, fail) => { resolve = ok; reject = fail; });
  return { promise, resolve, reject };
}
function mount(options: {
  load?: () => Promise<Values>;
  save?: (changes: Changes, signal?: AbortSignal) => Promise<{ success: boolean; error: string }>;
} = {}) {
  const writes: Changes[] = [];
  const api = {
    listEnvVars: options.load ?? (async () => []),
    async updateEnvVars(changes: Changes, signal?: AbortSignal) {
      writes.push(changes);
      return options.save ? options.save(changes, signal) : { success: true, error: '' };
    },
  } as unknown as ReturnType<typeof createInstanceAPI>;
  const root = new Element();
  const cleanup = mountCommandSettingsPage(root as unknown as HTMLElement, api);
  const el = (id: string) => root.querySelector(`#${id}`)!;
  return { root, el, cleanup, writes };
}

// Defaults match the runtime: only an explicit lowercase "false" disables #fa.
for (const raw of [undefined, '', 'true', 'false', 'False']) {
  const page = mount({ load: async () => raw === undefined ? [] : [{ key: statusKey, value: raw, isSecret: false }] });
  assert.equal(page.el('cmd-save-btn').disabled, true, 'initial loading must prevent writes');
  assert.equal(page.el('cmd-runtime-status').disabled, true);
  page.el('cmd-save-btn').click();
  assert.equal(page.writes.length, 0);
  await settle();
  assert.equal(page.el('cmd-runtime-status').checked, raw !== 'false', `wrong default for ${String(raw)}`);
  assert.equal(page.el('cmd-runtime-status').disabled, false);
  assert.equal(page.el('cmd-prefix-input').value, '/');
  assert.equal(page.el('cmd-load-status').hidden, true);
  assert.ok(page.root.innerHTML.indexOf('id="cmd-runtime-status"') < page.root.innerHTML.indexOf('id="cmd-prefix-input"'));
  assert.match(page.root.innerHTML, /一旦输入 FrostAgent 预留的调试语句“#fa”，将会促使 Bot 输出基本运行信息。/);
  assert.match(page.root.innerHTML, /id="cmd-runtime-status" role="switch" aria-label="输出FrostAgent默认运行信息"/);
  page.cleanup();
}
assert.equal(dedicatedSettingKeys.has(statusKey), true, 'dedicated status setting must not appear as a duplicate generic control');

// A failed load never turns an unknown persisted setting into an editable default.
let failLoad = true;
const failedLoad = mount({ load: async () => {
  if (failLoad) throw new Error('synthetic initial failure');
  return [{ key: statusKey, value: 'false', isSecret: false }, { key: 'ADMIN_COMMAND_PREFIX', value: 'execute', isSecret: false }];
} });
await settle();
assert.equal(failedLoad.el('cmd-runtime-status').disabled, true);
assert.equal(failedLoad.el('cmd-prefix-input').disabled, true);
assert.equal(failedLoad.el('cmd-reset-default-btn').disabled, true);
assert.equal(failedLoad.el('cmd-save-btn').disabled, true);
assert.equal(failedLoad.el('cmd-retry-btn').hidden, false);
assert.equal(failedLoad.el('cmd-retry-btn').disabled, false);
failedLoad.el('cmd-save-btn').click();
assert.equal(failedLoad.writes.length, 0);
assert.match(errors.at(-1)!, /synthetic initial failure/);
failLoad = false;
failedLoad.el('cmd-retry-btn').click();
await settle();
assert.equal(failedLoad.el('cmd-runtime-status').checked, false);
assert.equal(failedLoad.el('cmd-prefix-input').value, 'execute');
assert.match(failedLoad.el('cmd-examples-list').innerHTML, /@Bot execute reset/);
failedLoad.el('cmd-reset-default-btn').click();
assert.equal(failedLoad.el('cmd-prefix-input').value, '/');
assert.equal(failedLoad.el('cmd-runtime-status').checked, false, 'prefix reset must not enable status reporting');
failedLoad.cleanup();

// Prefix and toggle are one atomic batch; failures retain the entire edit for retry.
let failSave = true;
let transportFailure = false;
let heldSave: ReturnType<typeof deferred<{ success: boolean; error: string }>> | undefined;
let saveSignal: AbortSignal | undefined;
const page = mount({ save: async (_changes, signal) => {
  saveSignal = signal;
  if (heldSave) return heldSave.promise;
  if (transportFailure) throw new Error('synthetic lost response');
  return failSave ? { success: false, error: 'synthetic atomic failure' } : { success: true, error: '' };
} });
await settle();
page.el('cmd-runtime-status').toggle();
page.el('cmd-prefix-input').value = ' ! ';
page.el('cmd-prefix-input').emit('input');
assert.equal(page.writes.length, 0, 'editing only changes the local draft');
page.el('cmd-save-btn').click();
await settle();
assert.deepEqual(page.writes[0], [
  { key: 'ADMIN_COMMAND_PREFIX', value: '!', isSecret: false },
  { key: statusKey, value: 'false', isSecret: false },
]);
assert.match(errors.at(-1)!, /synthetic atomic failure/);
assert.equal(page.el('cmd-runtime-status').checked, false);
assert.equal(page.el('cmd-prefix-input').value, ' ! ');
assert.equal(page.el('cmd-save-btn').disabled, false);
transportFailure = true;
page.el('cmd-save-btn').click();
await settle();
assert.match(errors.at(-1)!, /synthetic lost response/);
assert.deepEqual(page.writes.at(-1), page.writes[0], 'unknown write outcome must retain both desired values');
transportFailure = false;
failSave = false;
heldSave = deferred();
page.el('cmd-save-btn').click();
assert.equal(page.el('cmd-runtime-status').disabled, true);
assert.equal(page.el('cmd-prefix-input').disabled, true);
assert.equal(page.el('cmd-reset-default-btn').disabled, true);
const before = page.writes.length;
page.el('cmd-save-btn').click();
page.el('cmd-runtime-status').toggle();
page.el('cmd-reset-default-btn').click();
assert.equal(page.el('cmd-runtime-status').checked, false, 'pending saves must not overwrite a newer toggle');
assert.equal(page.el('cmd-prefix-input').value, ' ! ');
assert.equal(page.writes.length, before, 'pending save must prevent duplicate submission');
heldSave.resolve({ success: true, error: '' });
await settle();
assert.equal(page.el('cmd-prefix-input').value, '!');
assert.equal(page.el('cmd-runtime-status').disabled, false);
assert.match(successes.at(-1)!, /指令设置已保存/);
heldSave = undefined;
page.el('cmd-runtime-status').toggle();
page.el('cmd-save-btn').click();
await settle();
assert.equal(page.writes.at(-1)?.find(({ key }) => key === statusKey)?.value, 'true');

// Leaving while saving aborts the page request and ignores all late feedback.
heldSave = deferred();
page.el('cmd-runtime-status').toggle();
page.el('cmd-save-btn').click();
const successCount = successes.length;
const errorCount = errors.length;
page.cleanup();
assert.equal(saveSignal?.aborted, true);
heldSave.resolve({ success: true, error: '' });
await settle();
assert.equal(successes.length, successCount);
assert.equal(errors.length, errorCount);

const lateLoad = deferred<Values>();
const unloaded = mount({ load: () => lateLoad.promise });
unloaded.cleanup();
lateLoad.resolve([{ key: statusKey, value: 'true', isSecret: false }]);
await settle();
assert.equal(unloaded.el('cmd-runtime-status').disabled, true);
assert.equal(unloaded.el('cmd-runtime-status').checked, false, 'late load must not modify an unmounted page');

// Exercise the real instance-bound API, not just a mocked page contract.
const originalFetch = globalThis.fetch;
const routes: string[] = [];
instanceState.select({ id: 'a1b2c3d4', name: 'a', enabled: true, created_at: '' });
let inFlightSignal: AbortSignal | undefined;
let requestStarted!: () => void;
const requestReady = new Promise<void>((resolve) => { requestStarted = resolve; });
globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = input instanceof Request ? input.url : String(input);
  routes.push(new URL(url).pathname);
  if (url.endsWith('/ListEnvVars')) {
    return new Response(JSON.stringify({ envVars: [{ key: statusKey, value: 'false' }] }), { headers: { 'Content-Type': 'application/json' } });
  }
  inFlightSignal = input instanceof Request ? input.signal : init?.signal ?? undefined;
  requestStarted();
  return new Promise<Response>((_resolve, reject) => {
    inFlightSignal?.addEventListener('abort', () => reject(inFlightSignal?.reason), { once: true });
  });
};
try {
  const root = new Element();
  const cleanup = mountCommandSettingsPage(root as unknown as HTMLElement);
  await settle();
  assert.equal(root.querySelector('#cmd-runtime-status')!.checked, false);
  root.querySelector('#cmd-runtime-status')!.toggle();
  root.querySelector('#cmd-save-btn')!.click();
  await requestReady;
  instanceState.select({ id: 'b1c2d3e4', name: 'b', enabled: true, created_at: '' });
  cleanup();
  await settle();
  assert.equal(inFlightSignal?.aborted, true, 'instance switch must abort the old settings request');
  assert.deepEqual(routes, [
    '/instances/a1b2c3d4/frostagent.v1.SettingsService/ListEnvVars',
    '/instances/a1b2c3d4/frostagent.v1.SettingsService/UpdateEnvVars',
  ], 'old page must never write into the newly selected instance');
} finally {
  globalThis.fetch = originalFetch;
  instanceState.select(null);
}

console.log('PASS: command settings defaults, atomic saves, retries, and instance lifecycle');
