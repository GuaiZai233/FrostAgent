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
let savedProvider = '';
let providers = [
  { id: 'provider-a', displayName: 'Provider <A>', available: true, baseUrl: 'https://newapi.example/v1' },
  { id: 'provider-b', displayName: 'Provider B', available: true },
  { id: 'no-key', displayName: 'No key', available: false },
];
let failSave = false;
let deferSave = false;
let releaseSave: (() => void) | undefined;
let overviewLoads = 0;
let deferGet = false;
let releaseGet: (() => void) | undefined;
const saves: Record<string, unknown>[] = [];
const toolUpdates: Record<string, boolean> = {};
globalThis.fetch = async (input, init) => {
  const path = new URL(input instanceof Request ? input.url : String(input), dom.window.location.origin).pathname;
  assert.ok(path === '/api/version' || path === '/api/instances' || path.startsWith(`/instances/${instance.id}/`), 'balance request escaped its instance');
  if (path === '/api/version') return Response.json({ display: 'synthetic-version' });
  if (path === '/api/instances') return Response.json({ instances: [instance] });
  if (path.endsWith('/GetOverview')) { overviewLoads++; return Response.json({ status: 2, tools: [
    { name: 'ban_user', description: 'Synthetic ordinary tool', enabled: true, available: true },
    { name: 'inspect_message_image', description: 'Runtime-gated tool', enabled: true, available: false },
    { name: 'balance_query', description: 'Query balance', enabled: true, available: !!savedKey || !!savedProvider, configurable: true },
  ] }); }
  if (path.endsWith('/SetToolEnabled')) {
    const body = await (input instanceof Request ? input : new Request(new URL(path, dom.window.location.origin), init)).json();
    toolUpdates[body.name] = body.enabled ?? false;
    return Response.json({});
  }
  if (path.endsWith('/GetBalanceQueryConfig')) {
    if (deferGet) await new Promise<void>((resolve) => { releaseGet = resolve; });
    return Response.json({ curl: savedCurl, apiKeyConfigured: !!savedKey || !!savedProvider, providerId: savedProvider, providers });
  }
  assert.ok(path.endsWith('/SaveBalanceQueryConfig'), path);
  const body = await (input instanceof Request ? input : new Request(new URL(path, dom.window.location.origin), init)).json();
  saves.push(body);
  if (deferSave) await new Promise<void>((resolve) => { releaseSave = resolve; });
  if (failSave) return Response.json({ code: 'invalid_argument', message: 'synthetic invalid curl' }, { status: 400 });
  // Mirrors the backend rule: a blank key may only be reused when the request endpoint is unchanged.
  const endpointOf = (curl: string) => curl.match(/https?:\/\/\S+/)?.[0] ?? '';
  if (!body.apiKey && !body.clearApiKey && !body.providerId && savedKey && endpointOf(body.curl) !== endpointOf(savedCurl)) {
    return Response.json({ code: 'invalid_argument', message: '请求地址已变更，请重新输入 Key，不会复用原有密钥' }, { status: 400 });
  }
  savedCurl = body.curl;
  if (body.clearApiKey) {
    savedKey = '';
    savedProvider = '';
  } else {
    savedProvider = body.providerId || '';
    if (savedProvider) savedKey = ''; else if (body.apiKey) savedKey = body.apiKey;
  }
  return Response.json({ curl: savedCurl, apiKeyConfigured: !!savedKey || !!savedProvider, providerId: savedProvider, providers });
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
  assert.equal(root.querySelectorAll('.overview-tool-configurable').length, 1);
  assert.equal(root.querySelector('.overview-tool-name .overview-tool-configurable')?.textContent, '可配置', 'configuration label must be below the configurable tool name');
  const balanceCard = [...root.querySelectorAll<HTMLElement>('.overview-tool-card')].find((card) => card.textContent?.includes('balance_query'))!;
  const balanceToggle = balanceCard.querySelector<HTMLInputElement>('[data-tool-toggle="balance_query"]')!;
  assert.equal(balanceToggle.checked, false, 'unconfigured balance_query must render as off');
  assert.equal(balanceCard.querySelector('.overview-tool-unconfigured')?.textContent, '未配置');
  const runtimeGatedCard = [...root.querySelectorAll<HTMLElement>('.overview-tool-card')].find((card) => card.textContent?.includes('inspect_message_image'))!;
  const runtimeGatedToggle = runtimeGatedCard.querySelector<HTMLInputElement>('[data-tool-toggle="inspect_message_image"]')!;
  assert.equal(runtimeGatedToggle.checked, true, 'non-balance unavailable tools keep the persisted enabled state');
  assert.equal(runtimeGatedCard.querySelector('.overview-tool-unconfigured'), null);
  runtimeGatedToggle.checked = false;
  runtimeGatedToggle.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  await flush();
  assert.equal(toolUpdates['inspect_message_image'], false, 'non-balance unavailable tool must still update through the API');
  assert.equal(document.querySelector('.fa-toast-error')?.textContent?.includes('请先点击卡片') ?? false, false);
  balanceToggle.checked = true;
  balanceToggle.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  await flush();
  assert.equal(balanceToggle.checked, false, 'forced enable must be reverted');
  assert.match(document.querySelector('.fa-toast-error')?.textContent ?? '', /请先点击卡片后进行配置/);
  assert.equal(toolUpdates['balance_query'], undefined, 'forced balance_query enable must not reach the API');
  await openConfig();
  const clearButton = document.querySelector<HTMLButtonElement>('#balance-query-clear-key')!;
  assert.ok(clearButton, 'clear-key action must exist');
  assert.equal(clearButton.disabled, true, 'clear-key action must be disabled without a stored key');
  assert.equal(document.querySelector<HTMLElement>('#balance-query-providers')!.hidden, true);
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
  const configuredCard = [...root.querySelectorAll<HTMLElement>('.overview-tool-card')].find((card) => card.textContent?.includes('balance_query'))!;
  assert.equal(configuredCard.querySelector('.overview-tool-unconfigured'), null);
  assert.equal(configuredCard.querySelector<HTMLInputElement>('[data-tool-toggle="balance_query"]')!.checked, true);
  await openConfig();
  key = document.querySelector<HTMLInputElement>('#balance-query-key')!;
  assert.equal(key.value, '', 'stored key must never populate the frontend field');
  assert.match(key.placeholder, /留空保留/);
  assert.equal(document.querySelector<HTMLButtonElement>('#balance-query-clear-key')!.disabled, false);
  assert.equal(document.querySelector<HTMLTextAreaElement>('#balance-query-curl')!.value, customCurl);
  await save();
  assert.equal(savedKey, 'synthetic-ui-balance-key');
  assert.ok(!saves.at(-1)!.apiKey, 'blank field must preserve the backend key');
  await openConfig();
  const reuse = document.querySelector<HTMLInputElement>('#balance-query-reuse-key')!;
  reuse.click();
  assert.equal(document.querySelector<HTMLInputElement>('#balance-query-key')!.disabled, true);
  assert.equal(document.querySelector<HTMLButtonElement>('#balance-query-clear-key')!.disabled, true);
  assert.equal(document.querySelector<HTMLElement>('#balance-query-manual-key')!.hidden, true);
  assert.equal(document.querySelector<HTMLElement>('#balance-query-providers')!.hidden, false);
  const savesBeforeSelection = saves.length;
  await save();
  assert.equal(saves.length, savesBeforeSelection, 'reuse without selecting a provider must not submit');
  assert.match(document.querySelector('#balance-query-error')!.textContent!, /请选择/);
  assert.equal(document.querySelector<HTMLInputElement>('input[value="no-key"]')!.disabled, true);
  assert.ok(document.querySelector('#balance-query-providers')!.textContent!.includes('Provider <A>'), 'provider name must remain plain text');
  assert.equal(document.querySelector('#balance-query-providers A'), null);
  document.querySelector<HTMLInputElement>('input[value="provider-a"]')!.click();
  document.querySelector<HTMLInputElement>('input[value="provider-b"]')!.click();
  assert.equal(document.querySelectorAll('input[name="balance-query-provider"]:checked').length, 1);
  await save();
  assert.equal(savedProvider, 'provider-b');
  assert.equal(savedKey, '');
  assert.equal(saves.at(-1)!.providerId, 'provider-b');
  assert.ok(!saves.at(-1)!.apiKey, 'provider reuse must not send a credential');
  assert.notEqual(saves.at(-1)!.clearApiKey, true, 'provider reuse must not clear the key');
  await openConfig();
  assert.equal(document.querySelector<HTMLInputElement>('#balance-query-reuse-key')!.checked, true);
  assert.equal(document.querySelector<HTMLInputElement>('input[value="provider-b"]')!.checked, true);
  document.querySelector<HTMLInputElement>('#balance-query-reuse-key')!.click();
  await save();
  assert.match(document.querySelector('#balance-query-error')!.textContent!, /请填写/);
  document.querySelector<HTMLInputElement>('#balance-query-key')!.value = 'synthetic-ui-manual-key';
  await save();
  assert.equal(savedProvider, '');
  assert.equal(savedKey, 'synthetic-ui-manual-key');
  for (const failure of [false, true]) {
    await openConfig();
    const dialog = document.querySelector<HTMLDialogElement>('dialog')!;
    const key = dialog.querySelector<HTMLInputElement>('#balance-query-key')!;
    key.value = 'synthetic-pending-key';
    if (failure) {
      dialog.querySelector<HTMLInputElement>('#balance-query-reuse-key')!.click();
      dialog.querySelector<HTMLInputElement>('input[value="provider-a"]')!.click();
    }
    deferSave = true;
    failSave = failure;
    const savesBefore = saves.length;
    const loadsBefore = overviewLoads;
    await save();
    assert.equal(saves.length, savesBefore + 1);
    assert.equal(dialog.querySelector<HTMLButtonElement>('#balance-query-save')!.disabled, true);
    assert.equal(key.disabled, true);
    assert.equal(dialog.querySelector<HTMLInputElement>('#balance-query-reuse-key')!.disabled, true);
    assert.ok([...dialog.querySelectorAll<HTMLInputElement>('input[name="balance-query-provider"]')].every((input) => input.disabled));
    assert.equal(dialog.querySelector<HTMLTextAreaElement>('#balance-query-curl')!.disabled, true);
    const presetSelect = dialog.querySelector<HTMLSelectElement>('#balance-query-preset')!;
    assert.equal(presetSelect.disabled, true, 'preset select must be disabled while a save is pending');
    const pendingCurl = dialog.querySelector<HTMLTextAreaElement>('#balance-query-curl')!.value;
    presetSelect.value = 'deepseek';
    presetSelect.dispatchEvent(new dom.window.Event('change'));
    assert.equal(dialog.querySelector<HTMLTextAreaElement>('#balance-query-curl')!.value, pendingCurl, 'pending save curl must not change');
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
      assert.equal(key.disabled, true, 'provider mode must remain selected after failure');
      assert.equal(dialog.querySelector<HTMLInputElement>('input[value="provider-a"]')!.checked, true);
      assert.equal(dialog.querySelector<HTMLInputElement>('input[value="provider-a"]')!.disabled, false);
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
  await openConfig();
  const clearButtonAfterSave = document.querySelector<HTMLButtonElement>('#balance-query-clear-key')!;
  assert.equal(clearButtonAfterSave.disabled, false, 'clear-key action must be enabled for a stored key');
  clearButtonAfterSave.click();
  assert.equal(document.querySelector<HTMLInputElement>('#balance-query-key')!.value, '');
  assert.match(document.querySelector('#balance-query-key-hint')!.textContent!, /清除/);
  await save();
  assert.equal(saves.at(-1)!.clearApiKey, true);
  assert.equal(savedKey, '');
  assert.equal(document.querySelectorAll('dialog').length, 0);
  await flush();
  const clearedCard = [...root.querySelectorAll<HTMLElement>('.overview-tool-card')].find((card) => card.textContent?.includes('balance_query'))!;
  assert.equal(clearedCard.querySelector('.overview-tool-unconfigured')?.textContent, '未配置');
  assert.equal(clearedCard.querySelector<HTMLInputElement>('[data-tool-toggle="balance_query"]')!.checked, false);
  providers = [];
  savedProvider = 'deleted-provider';
  await openConfig();
  assert.match(document.querySelector('#balance-query-providers')!.textContent!, /暂无 Provider/);
  assert.match(document.querySelector('#balance-query-providers')!.textContent!, /原 Provider 已不可用/);
  const savesBeforeMissingProvider = saves.length;
  await save();
  assert.equal(saves.length, savesBeforeMissingProvider, 'missing provider must not silently fall back to a manual credential');
  document.querySelector<HTMLButtonElement>('dialog .dialog-close-btn')!.click();
  await flush();
  {
    // Earlier cases empty the provider list and set a deleted provider; restore a usable list for these cases.
    providers = [
      { id: 'provider-a', displayName: 'Provider <A>', available: true, baseUrl: 'https://newapi.example/v1' },
      { id: 'provider-b', displayName: 'Provider B', available: true },
    ];
    savedProvider = '';
    // Preset selection fills the curl template and the matching key placeholder.
    await openConfig();
    const presetDialog = document.querySelector<HTMLDialogElement>('dialog')!;
    const preset = presetDialog.querySelector<HTMLSelectElement>('#balance-query-preset')!;
    const presetCurl = presetDialog.querySelector<HTMLTextAreaElement>('#balance-query-curl')!;
    const presetKey = presetDialog.querySelector<HTMLInputElement>('#balance-query-key')!;
    const presetHint = presetDialog.querySelector<HTMLElement>('#balance-query-preset-hint')!;
    const choose = (value: string) => { preset.value = value; preset.dispatchEvent(new dom.window.Event('change')); };
    assert.deepEqual([...preset.options].map((option) => option.value), ['', 'deepseek', 'newapi-key', 'newapi-account']);
    choose('newapi-key');
    assert.match(presetCurl.value, /\/api\/usage\/token\/\s*\\/);
    assert.match(presetCurl.value, /\{\{API_KEY\}\}/);
    assert.equal(presetKey.placeholder, 'sk-xxx');
    assert.match(presetHint.textContent!, /unlimited_quota/);
    choose('newapi-account');
    assert.match(presetCurl.value, /\/api\/user\/self\s*\\/);
    assert.equal(presetKey.placeholder, 'nap_xxx');
    assert.match(presetHint.textContent!, /profile:read/);
    choose('');
    assert.equal(presetHint.hidden, true, 'custom option must hide the preset hint');
    const testButton = presetDialog.querySelector<HTMLButtonElement>('#balance-query-test')!;
    const cancelButton = [...presetDialog.querySelectorAll<HTMLButtonElement>('.dialog-close-btn')].find((button) => button.textContent!.trim() === '取消')!;
    assert.equal(testButton.textContent!.trim(), '测试连接');
    const clearButton = presetDialog.querySelector<HTMLButtonElement>('#balance-query-clear-config')!;
    assert.ok(clearButton.textContent!.includes('清除配置'), 'clear button must be labelled 清除配置');
    assert.ok(clearButton.compareDocumentPosition(testButton) & dom.window.Node.DOCUMENT_POSITION_FOLLOWING, '清除配置 must be the left-most footer action');
    assert.ok(testButton.compareDocumentPosition(cancelButton) & dom.window.Node.DOCUMENT_POSITION_FOLLOWING, '测试连接 must sit to the left of 取消');
    choose('newapi-key');
    presetCurl.value = "curl https://hand.example/balance -H 'Authorization: Bearer {{API_KEY}}'";
    presetCurl.dispatchEvent(new dom.window.Event('input'));
    assert.equal(preset.value, '', 'hand-editing the template must return the selector to 自定义');
    assert.equal(presetHint.hidden, true);

    // New API endpoint reuse: the option appears only for New API presets; enabling it hides the curl box,
    // forces the provider key, and builds the request from the provider's scheme and host (path removed).
    const endpointRowEl = presetDialog.querySelector<HTMLElement>('#balance-query-endpoint-row')!;
    const useEndpointEl = presetDialog.querySelector<HTMLInputElement>('#balance-query-use-endpoint')!;
    const reuseKeyEl = presetDialog.querySelector<HTMLInputElement>('#balance-query-reuse-key')!;
    choose('newapi-key');
    assert.equal(endpointRowEl.hidden, false);
    choose('deepseek');
    assert.equal(endpointRowEl.hidden, true, 'endpoint reuse is only offered for New API presets');
    // Quota conversion: only for New API presets, default ratio 500000, sent with the save request.
    const quotaBoxEl = presetDialog.querySelector<HTMLElement>('#balance-query-quota')!;
    const quotaEnabledEl = presetDialog.querySelector<HTMLInputElement>('#balance-query-quota-enabled')!;
    const quotaRatioEl = presetDialog.querySelector<HTMLInputElement>('#balance-query-quota-ratio')!;
    choose('deepseek');
    assert.equal(quotaBoxEl.hidden, true, 'quota conversion is only offered for New API presets');
    choose('newapi-key');
    assert.equal(quotaBoxEl.hidden, false);
    assert.equal(quotaRatioEl.value, '500000', 'default quota ratio is 500000');
    quotaEnabledEl.checked = true;
    quotaEnabledEl.dispatchEvent(new dom.window.Event('change'));
    assert.equal(quotaRatioEl.closest<HTMLElement>('#balance-query-quota-ratio-row')!.hidden, false, 'ratio input shows when enabled');
    quotaEnabledEl.checked = false;
    quotaEnabledEl.dispatchEvent(new dom.window.Event('change'));
    choose('newapi-key');
    useEndpointEl.checked = true;
    useEndpointEl.dispatchEvent(new dom.window.Event('change'));
    assert.equal(presetCurl.hidden, true, 'enabling endpoint reuse hides the curl box');
    assert.equal(reuseKeyEl.checked, true);
    assert.equal(reuseKeyEl.disabled, true);
    presetDialog.querySelector<HTMLInputElement>('input[value="provider-a"]')!.click();
    assert.equal(presetCurl.value, "curl https://newapi.example/api/usage/token/ \\\n  -H 'Authorization: Bearer {{API_KEY}}'");
    useEndpointEl.checked = false;
    useEndpointEl.dispatchEvent(new dom.window.Event('change'));
    assert.equal(presetCurl.hidden, false, 'disabling endpoint reuse shows the curl box again');
    assert.equal(reuseKeyEl.disabled, false);
    presetDialog.querySelector<HTMLButtonElement>('.dialog-close-btn')!.click();
    await flush();

    // Switching a stored manual key to another host must not silently reuse the old secret.
    savedProvider = '';
    savedKey = 'synthetic-stored-deepseek-key';
    await openConfig();
    const hostDialog = document.querySelector<HTMLDialogElement>('dialog')!;
    const hostPreset = hostDialog.querySelector<HTMLSelectElement>('#balance-query-preset')!;
    const hostKey = hostDialog.querySelector<HTMLInputElement>('#balance-query-key')!;
    const hostError = hostDialog.querySelector<HTMLElement>('#balance-query-error')!;
    hostPreset.value = 'newapi-key';
    hostPreset.dispatchEvent(new dom.window.Event('change'));
    assert.equal(hostKey.required, true, 'preset change with a stored key must require re-entry');
    const savesBeforeHostSwitch = saves.length;
    hostDialog.querySelector<HTMLFormElement>('#balance-query-form')!.dispatchEvent(new dom.window.Event('submit', { cancelable: true }));
    await flush();
    assert.equal(saves.length, savesBeforeHostSwitch, 'stored key must not be reused for a new host');
    assert.match(hostError.textContent!, /请重新输入/);
    hostKey.value = 'sk-synthetic-new-host-key';
    hostDialog.querySelector<HTMLFormElement>('#balance-query-form')!.dispatchEvent(new dom.window.Event('submit', { cancelable: true }));
    await flush();
    assert.equal(saves.length, savesBeforeHostSwitch + 1);
    assert.equal(saves.at(-1)!.apiKey, 'sk-synthetic-new-host-key');

    // Editing the curl host by hand (no preset) is the same destination change and must also block reuse.
    await openConfig();
    const editDialog = document.querySelector<HTMLDialogElement>('dialog')!;
    const editCurl = editDialog.querySelector<HTMLTextAreaElement>('#balance-query-curl')!;
    const editError = editDialog.querySelector<HTMLElement>('#balance-query-error')!;
    const savesBeforeManualHost = saves.length;
    editCurl.value = "curl https://attacker.example/api/user/self \\\n  -H 'Authorization: Bearer {{API_KEY}}'";
    editCurl.dispatchEvent(new dom.window.Event('input'));
    editDialog.querySelector<HTMLFormElement>('#balance-query-form')!.dispatchEvent(new dom.window.Event('submit', { cancelable: true }));
    await flush();
    // Custom template: a blank key is sent, and the backend refuses to reuse the stored key for a new host.
    assert.equal(saves.length, savesBeforeManualHost + 1);
    assert.ok(!saves.at(-1)!.apiKey, 'blank custom key is allowed in the form');
    assert.match(editError.textContent!, /请重新输入/);
    assert.equal(savedKey, 'sk-synthetic-new-host-key', 'refused edit must not change the stored key');
    // A path-only change on a different endpoint is refused the same way.
    editCurl.value = "curl https://你的NewAPI地址/api/user/self \\\n  -H 'Authorization: Bearer {{API_KEY}}'";
    editCurl.dispatchEvent(new dom.window.Event('input'));
    editDialog.querySelector<HTMLFormElement>('#balance-query-form')!.dispatchEvent(new dom.window.Event('submit', { cancelable: true }));
    await flush();
    assert.match(editError.textContent!, /请重新输入/);
    editDialog.querySelector<HTMLInputElement>('#balance-query-key')!.value = 'sk-synthetic-path-key';
    editDialog.querySelector<HTMLFormElement>('#balance-query-form')!.dispatchEvent(new dom.window.Event('submit', { cancelable: true }));
    await flush();
    assert.equal(saves.at(-1)!.apiKey, 'sk-synthetic-path-key');
    savedKey = '';
  }

  {
    // The enabled quota ratio is sent with the save request; disabling it sends 0.
    await openConfig();
    const quotaDialog = document.querySelector<HTMLDialogElement>('dialog')!;
    const quotaPreset = quotaDialog.querySelector<HTMLSelectElement>('#balance-query-preset')!;
    quotaPreset.value = 'newapi-key';
    quotaPreset.dispatchEvent(new dom.window.Event('change'));
    quotaDialog.querySelector<HTMLInputElement>('#balance-query-quota-enabled')!.checked = true;
    quotaDialog.querySelector<HTMLInputElement>('#balance-query-quota-enabled')!.dispatchEvent(new dom.window.Event('change'));
    quotaDialog.querySelector<HTMLInputElement>('#balance-query-quota-ratio')!.value = '500000';
    quotaDialog.querySelector<HTMLInputElement>('#balance-query-key')!.value = 'sk-synthetic-quota-key';
    const quotaBefore = saves.length;
    quotaDialog.querySelector<HTMLFormElement>('#balance-query-form')!.dispatchEvent(new dom.window.Event('submit', { cancelable: true }));
    await flush();
    assert.equal(saves.length, quotaBefore + 1);
    assert.equal(saves.at(-1)!.quotaPerUnit, 500000, 'enabled quota ratio must be sent with the save');
    savedKey = '';
    await openConfig();
    const offDialog = document.querySelector<HTMLDialogElement>('dialog')!;
    const offPreset = offDialog.querySelector<HTMLSelectElement>('#balance-query-preset')!;
    offPreset.value = 'newapi-account';
    offPreset.dispatchEvent(new dom.window.Event('change'));
    offDialog.querySelector<HTMLInputElement>('#balance-query-quota-enabled')!.checked = false;
    offDialog.querySelector<HTMLInputElement>('#balance-query-key')!.value = 'nap_synthetic_quota_key';
    const offBefore = saves.length;
    offDialog.querySelector<HTMLFormElement>('#balance-query-form')!.dispatchEvent(new dom.window.Event('submit', { cancelable: true }));
    await flush();
    assert.equal(saves.at(-1)!.quotaPerUnit ?? 0, 0, 'disabled quota conversion sends 0');
    assert.equal(saves.length, offBefore + 1);
    savedKey = '';
  }

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
console.log('PASS: balance query defaults, unavailable toggle, clear-key, provider reuse, credentials, pending-save dismissal, preset selection, failure retry and instance lifecycle');
