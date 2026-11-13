import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { setImmediate } from 'node:timers/promises';
import { mountOverviewPage } from '../src/pages/overview';
import { instanceState } from '../src/instance-state';

const { JSDOM } = createRequire(resolve('package.json'))('jsdom');
const dom = new JSDOM('<body><main id="root"></main></body>', { url: 'http://panel.example/' });
const originals = { window: globalThis.window, document: globalThis.document, Event: globalThis.Event, fetch: globalThis.fetch };
Object.assign(globalThis, { window: dom.window, document: dom.window.document, Event: dom.window.Event });
dom.window.HTMLDialogElement.prototype.showModal = function () { this.open = true; };
dom.window.HTMLDialogElement.prototype.close = function () { this.open = false; };
dom.window.setInterval = () => 1;
const root = document.querySelector<HTMLElement>('#root')!;
const instance = { id: 'fixture-sleep', name: 'Sleep', enabled: true, created_at: '' };
let seconds = '';
let failSave = false;
let deferLoad = false;
let releaseLoad: (() => void) | undefined;
const writes: Record<string, unknown>[] = [];
globalThis.fetch = async (input, init) => {
  const path = new URL(input instanceof Request ? input.url : String(input), dom.window.location.origin).pathname;
  assert.ok(path === '/api/version' || path === '/api/instances' || path.startsWith(`/instances/${instance.id}/`), 'configuration escaped its instance');
  if (path === '/api/version') return Response.json({ display: 'synthetic-version' });
  if (path === '/api/instances') return Response.json({ instances: [instance] });
  if (path.endsWith('/GetOverview')) return Response.json({ status: 2, tools: [{ name: 'time_sleep', configurable: true, enabled: true, available: true }] });
  if (path.endsWith('/ListEnvVars')) {
    if (deferLoad) await new Promise<void>((resolve) => { releaseLoad = resolve; });
    return Response.json({ envVars: [{ key: 'TIME_SLEEP_TIMEOUT_SECONDS', value: seconds }] });
  }
  assert.ok(path.endsWith('/UpdateEnvVar'), path);
  const body = await (input instanceof Request ? input : new Request(new URL(path, dom.window.location.origin), init)).json();
  writes.push(body);
  if (failSave) return Response.json({ success: false, error: 'synthetic save failure' });
  seconds = body.value ?? '';
  return Response.json({ success: true });
};
const flush = async () => { for (let i = 0; i < 8; i++) await setImmediate(); };
const open = async () => { root.querySelector<HTMLButtonElement>('[data-tool-config="time_sleep"]')!.click(); await flush(); };
const submit = async () => { document.querySelector<HTMLFormElement>('#time-sleep-form')!.dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true })); await flush(); };
let cleanup = () => {};
try {
  instanceState.select(instance);
  cleanup = mountOverviewPage(root);
  await flush();
  await open();
  const input = document.querySelector<HTMLInputElement>('#time-sleep-timeout')!;
  assert.equal(input.placeholder, '60');
  input.value = '0';
  await submit();
  assert.equal(writes.length, 0);
  input.value = '90.5';
  failSave = true;
  await submit();
  assert.equal(input.value, '90.5');
  assert.match(document.querySelector('#time-sleep-error')!.textContent!, /synthetic save failure/);
  failSave = false;
  await submit();
  assert.deepEqual(writes.at(-1), { key: 'TIME_SLEEP_TIMEOUT_SECONDS', value: '90.5' });
  assert.equal(document.querySelectorAll('dialog').length, 0);
  await open();
  document.querySelector<HTMLInputElement>('#time-sleep-timeout')!.value = '';
  await submit();
  assert.equal(seconds, '');
  deferLoad = true;
  await open();
  assert.ok(releaseLoad);
  instanceState.select(null);
  releaseLoad!();
  await flush();
  assert.equal(document.querySelectorAll('dialog').length, 0);
} finally {
  cleanup();
  Object.assign(globalThis, originals);
  dom.window.close();
}
