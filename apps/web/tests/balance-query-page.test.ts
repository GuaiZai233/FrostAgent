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
const instance = { id: 'fixture-balance', name: 'Balance', enabled: true, created_at: '' };
const defaultCurl = "curl https://api.deepseek.com/user/balance -H 'Authorization: Bearer {{API_KEY}}'";
let savedCurl = defaultCurl;
let savedKey = '';
let failSave = false;
let deferGet = false;
let releaseGet: (() => void) | undefined;
const saves: Record<string, unknown>[] = [];
globalThis.fetch = async (input, init) => {
  const path = new URL(input instanceof Request ? input.url : String(input), dom.window.location.origin).pathname;
  assert.ok(path === '/api/instances' || path.startsWith(`/instances/${instance.id}/`), 'balance request escaped its instance');
  if (path === '/api/instances') return Response.json({ instances: [instance] });
  if (path.endsWith('/GetOverview')) return Response.json({ status: 2, tools: [
    { name: 'ban_user', description: 'Synthetic ordinary tool', enabled: true, available: true },
    { name: 'balance_query', description: 'Query balance', enabled: true, available: !!savedKey, configurable: true },
  ] });
  if (path.endsWith('/GetBalanceQueryConfig')) {
    if (deferGet) await new Promise<void>((resolve) => { releaseGet = resolve; });
    return Response.json({ curl: savedCurl, apiKeyConfigured: !!savedKey });
  }
  assert.ok(path.endsWith('/SaveBalanceQueryConfig'), path);
  const body = await (input instanceof Request ? input : new Request(new URL(path, dom.window.location.origin), init)).json();
  saves.push(body);
  if (failSave) return Response.json({ code: 'invalid_argument', message: 'synthetic invalid curl' }, { status: 400 });
  savedCurl = body.curl;
  if (body.clearApiKey) savedKey = ''; else if (body.apiKey) savedKey = body.apiKey;
  return Response.json({ curl: savedCurl, apiKeyConfigured: !!savedKey });
};
const flush = async () => { for (let i = 0; i < 8; i++) await setImmediate(); };
const openConfig = async () => { root.querySelector<HTMLButtonElement>('[data-tool-config="balance_query"]')!.click(); await flush(); };
const save = async () => { document.querySelector<HTMLFormElement>('#balance-query-form')!.dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true })); await flush(); };
let cleanup = () => {};
try {
  instanceState.select(instance);
  cleanup = mountOverviewPage(root);
  await flush();
  assert.equal(root.querySelectorAll('[data-tool-config]').length, 1, 'only configurable cards may open a configuration dialog');
  await openConfig();
  assert.equal(document.querySelector<HTMLTextAreaElement>('#balance-query-curl')!.value, defaultCurl);
  let key = document.querySelector<HTMLInputElement>('#balance-query-key')!;
  assert.equal(key.value, '');
  key.value = 'synthetic-ui-balance-key';
  failSave = true;
  await save();
  assert.match(document.querySelector('#balance-query-error')!.textContent!, /synthetic invalid curl/);
  assert.equal(key.value, 'synthetic-ui-balance-key', 'failed save must preserve the entered draft');
  failSave = false;
  const customCurl = "curl http://127.0.0.1:9000/balance -H 'X-API-Key: {{API_KEY}}'";
  document.querySelector<HTMLTextAreaElement>('#balance-query-curl')!.value = customCurl;
  await save();
  assert.equal(document.querySelectorAll('dialog').length, 0);
  assert.equal(savedKey, 'synthetic-ui-balance-key');
  await openConfig();
  key = document.querySelector<HTMLInputElement>('#balance-query-key')!;
  assert.equal(key.value, '', 'stored key must never populate the frontend field');
  assert.match(key.placeholder, /留空保留/);
  assert.equal(document.querySelector<HTMLTextAreaElement>('#balance-query-curl')!.value, customCurl);
  await save();
  assert.equal(savedKey, 'synthetic-ui-balance-key');
  assert.ok(!saves.at(-1)!.apiKey, 'blank field must preserve the backend key');
  await openConfig();
  const clear = document.querySelector<HTMLInputElement>('#balance-query-clear-key')!;
  clear.click();
  assert.equal(document.querySelector<HTMLInputElement>('#balance-query-key')!.disabled, true);
  await save();
  assert.equal(savedKey, '');
  deferGet = true;
  await openConfig();
  cleanup();
  releaseGet?.();
  await flush();
  assert.equal(document.querySelectorAll('dialog').length, 0, 'late configuration response opened a dialog after unmount');
} finally {
  cleanup();
  instanceState.select(null);
  dom.window.close();
  Object.assign(globalThis, originals);
}
console.log('PASS: balance query default, custom curl, write-only key, failure retry, clearing and instance lifecycle');
