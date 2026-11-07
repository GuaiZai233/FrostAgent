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
let deferSave = false;
let releaseSave: (() => void) | undefined;
let overviewLoads = 0;
let deferGet = false;
let releaseGet: (() => void) | undefined;
const saves: Record<string, unknown>[] = [];
globalThis.fetch = async (input, init) => {
  const path = new URL(input instanceof Request ? input.url : String(input), dom.window.location.origin).pathname;
  assert.ok(path === '/api/version' || path === '/api/instances' || path.startsWith(`/instances/${instance.id}/`), 'balance request escaped its instance');
  if (path === '/api/version') return Response.json({ display: 'synthetic-version' });
  if (path === '/api/instances') return Response.json({ instances: [instance] });
  if (path.endsWith('/GetOverview')) { overviewLoads++; return Response.json({ status: 2, tools: [
    { name: 'ban_user', description: 'Synthetic ordinary tool', enabled: true, available: true },
    { name: 'balance_query', description: 'Query balance', enabled: true, available: !!savedKey, configurable: true },
  ] }); }
  if (path.endsWith('/GetBalanceQueryConfig')) {
    if (deferGet) await new Promise<void>((resolve) => { releaseGet = resolve; });
    return Response.json({ curl: savedCurl, apiKeyConfigured: !!savedKey });
  }
  assert.ok(path.endsWith('/SaveBalanceQueryConfig'), path);
  const body = await (input instanceof Request ? input : new Request(new URL(path, dom.window.location.origin), init)).json();
  saves.push(body);
  if (deferSave) await new Promise<void>((resolve) => { releaseSave = resolve; });
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
  for (const failure of [false, true]) {
    await openConfig();
    const dialog = document.querySelector<HTMLDialogElement>('dialog')!;
    const key = dialog.querySelector<HTMLInputElement>('#balance-query-key')!;
    key.value = 'synthetic-pending-key';
    deferSave = true;
    failSave = failure;
    const savesBefore = saves.length;
    const loadsBefore = overviewLoads;
    await save();
    assert.equal(saves.length, savesBefore + 1);
    assert.equal(dialog.querySelector<HTMLButtonElement>('#balance-query-save')!.disabled, true);
    assert.equal(key.disabled, true);
    assert.equal(dialog.querySelector<HTMLTextAreaElement>('#balance-query-curl')!.disabled, true);
    await save();
    assert.equal(saves.length, savesBefore + 1, 'pending save was submitted twice');
    for (const close of dialog.querySelectorAll<HTMLButtonElement>('.dialog-close-btn')) {
      close.click();
      assert.equal(dialog.isConnected, true, 'a button dismissed the pending save');
    }
    const escape = new dom.window.Event('cancel', { cancelable: true });
    dialog.dispatchEvent(escape);
    assert.equal(escape.defaultPrevented, true);
    assert.equal(dialog.isConnected, true, 'Escape dismissed the pending save');
    dialog.dispatchEvent(new dom.window.MouseEvent('click', { clientX: -1, clientY: -1 }));
    assert.equal(dialog.isConnected, true, 'backdrop dismissed the pending save');
    deferSave = false;
    releaseSave?.();
    await flush();
    if (failure) {
      assert.equal(dialog.isConnected, true, 'failed save lost the draft');
      assert.equal(key.value, 'synthetic-pending-key');
      assert.match(dialog.querySelector('#balance-query-error')!.textContent!, /synthetic invalid curl/);
      assert.equal(dialog.querySelector<HTMLButtonElement>('#balance-query-save')!.disabled, false);
      assert.equal(key.disabled, false);
      assert.equal(dialog.querySelector<HTMLTextAreaElement>('#balance-query-curl')!.disabled, false);
      assert.ok([...dialog.querySelectorAll<HTMLButtonElement>('.dialog-close-btn')].every((button) => !button.disabled));
      assert.equal(overviewLoads, loadsBefore, 'failed save refreshed the overview');
      dialog.querySelector<HTMLButtonElement>('.dialog-close-btn')!.click();
      assert.equal(dialog.isConnected, false, 'failed save could not be dismissed');
      await flush();
    } else {
      assert.equal(dialog.isConnected, false, 'successful save did not close the dialog');
      assert.equal(savedKey, 'synthetic-pending-key');
      assert.ok(document.querySelector('.fa-toast-success')?.textContent?.includes('额度查询配置已保存'), 'save success was not shown');
      assert.ok(overviewLoads > loadsBefore, 'successful save did not refresh the overview');
    }
  }
  failSave = false;
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
console.log('PASS: balance query defaults, credentials, pending-save dismissal, failure retry and instance lifecycle');
