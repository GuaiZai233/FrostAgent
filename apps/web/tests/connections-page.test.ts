import { createTestDOM, emit } from './dom-fixture';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers/promises';
import { createInstanceAPI } from '../src/api/client';
import { toast } from '../src/components/toast';
import { instanceState } from '../src/instance-state';
import { mountConnectionsPage } from '../src/pages/connections';

const dom = createTestDOM('http://localhost:8088');
try {
  const copies: string[] = [];
  Object.defineProperty(window, 'isSecureContext', { value: true });
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
  const prefixes = ['ONEBOT'];
  let values: Setting[] = prefixes.flatMap((prefix) => ['ENABLED', 'MODE', 'URL', 'TOKEN', 'INBOUND_TOKEN'].map((field) => ({
    key: `${prefix}_WS_${field}`, value: '', isSecret: field.endsWith('TOKEN'),
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
  let root = dom.root();
  let cleanup = mountConnectionsPage(root as unknown as HTMLElement, api);
  const el = (id: string) => {
    const found = root.querySelector(`#${id}`);
    assert.ok(found, `missing element ${id}`);
    return found;
  };
  const edit = (id: string, value: string) => {
    el(id).value = value;
    emit(el(id), id.endsWith('-mode') ? 'change' : 'input');
  };
  const enable = (adapter: string, checked: boolean) => {
    el(`${adapter}-enabled`).checked = checked;
    emit(el(`${adapter}-enabled`), 'change');
  };
  await setImmediate();
  assert.match(root.innerHTML, /Synthetic &lt;instance&gt;/, 'instance name must be escaped');
  assert.equal(root.querySelector('#astrbot-mode'), null, 'removed adapter has no controls');
  assert.equal(el('onebot-mode').value, 'reverse', 'missing mode must preserve existing server behavior');
  assert.equal(el('onebot-enabled').checked, true, 'missing enable flag preserves existing connections');
  assert.equal(el('onebot-enabled').type, 'checkbox');
  assert.equal(el('onebot-inbound_token').type, 'password');
  assert.equal(el('onebot-inbound_token').disabled, false);
  assert.equal(el('onebot-forward').hidden, true);
  assert.equal(el('connections-save').disabled, true);
  assert.equal(el('connections-restore').disabled, true);
  assert.equal(el('connections-draft').hidden, true);
  assert.equal(el('onebot-address').textContent, 'ws://localhost:1234/instances/a1b2c3d4/ws/onebot');
  el('onebot-copy').click();
  await setImmediate();
  assert.deepEqual(copies, [el('onebot-address').textContent]);

  edit('onebot-mode', 'forward');
  assert.equal(el('onebot-forward').hidden, false);
  assert.equal(el('onebot-reverse').hidden, true);
  assert.equal(el('onebot-copy').disabled, true, 'forward mode must not offer an inbound address');
  assert.equal(el('onebot-inbound_token').disabled, true);
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
  // Native single-line inputs sanitize newlines; bypass that setter only to
  // exercise the page's defensive validation of a malformed credential.
  Object.defineProperty(el('onebot-token'), 'value', {
    configurable: true,
    writable: true,
    value: '',
  });
  edit('onebot-token', 'synthetic\nheader');
  el('connections-save').click();
  await setImmediate();
  assert.equal(writes.length, 0, 'header newlines must be rejected');
  Reflect.deleteProperty(el('onebot-token'), 'value');
  edit('onebot-token', 'synthetic-onebot-token');
  assert.equal(el('connections-draft').hidden, false);
  assert.ok(!root.innerHTML.includes('synthetic-onebot-token'), 'secret values must not be interpolated into HTML');

  failSave = true;
  el('connections-save').click();
  await setImmediate();
  assert.equal(writes.length, 1);
  assert.equal(writes[0].length, 3, 'connection fields must be saved in one atomic batch');
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

  root = dom.root();
  cleanup = mountConnectionsPage(root as unknown as HTMLElement, api);
  await setImmediate();
  assert.equal(el('onebot-mode').value, 'reverse');
  assert.equal(el('onebot-url').value, 'wss://upstream.invalid/onebot', 'remount must retain the forward settings');
  edit('onebot-mode', 'forward');
  el('connections-save').click();
  await setImmediate();
  edit('onebot-token', '');
  el('connections-save').click();
  await setImmediate();
  assert.deepEqual(writes.at(-1), [{ key: 'ONEBOT_WS_TOKEN', value: null, isSecret: true }], 'clearing a token must use the clearKeys API');

  edit('onebot-token', 'synthetic-onebot-token');
  edit('onebot-mode', 'reverse');
  el('connections-save').click();
  await setImmediate();

  const copiesBeforeDisable = copies.length;
  const writesBeforeDisable = writes.length;
  enable('onebot', false);
  assert.equal(el('onebot-mode').disabled, true);
  assert.equal(el('onebot-copy').disabled, true);
  assert.equal(el('onebot-disabled-hint').hidden, false);
  assert.equal(el('onebot-enabled').disabled, false, 'disabled adapters must remain re-enableable');
  el('onebot-copy').click();
  assert.equal(copies.length, copiesBeforeDisable);
  assert.equal(writes.length, writesBeforeDisable, 'checkbox changes remain a local draft');
  failSave = true;
  el('connections-save').click();
  await setImmediate();
  assert.equal(el('onebot-enabled').checked, false, 'failed save retains the enable draft');
  assert.equal(el('connections-save').disabled, false);
  failSave = false;
  el('connections-save').click();
  await setImmediate();
  assert.deepEqual(writes.at(-1), prefixes.map((prefix) => ({ key: `${prefix}_WS_ENABLED`, value: 'false', isSecret: false })));
  assert.equal(el('onebot-url').value, 'wss://upstream.invalid/onebot');
  assert.equal(el('onebot-token').value, 'synthetic-onebot-token');
  cleanup();
  root = dom.root();
  cleanup = mountConnectionsPage(root as unknown as HTMLElement, api);
  await setImmediate();
  assert.equal(el('onebot-enabled').checked, false, 'disabled flag must survive reload');
  assert.equal(el('onebot-mode').value, 'reverse');
  enable('onebot', true);
  el('connections-restore').click();
  assert.equal(el('onebot-enabled').checked, false, 'restore must reset checkbox state');
  assert.equal(el('onebot-copy').disabled, true);
  enable('onebot', true);
  el('connections-save').click();
  await setImmediate();
  assert.equal(el('onebot-copy').disabled, false);
  assert.deepEqual(writes.at(-1), prefixes.map((prefix) => ({ key: `${prefix}_WS_ENABLED`, value: 'true', isSecret: false })));
  edit('onebot-mode', 'forward');
  edit('onebot-url', '');
  enable('onebot', false);
  el('connections-save').click();
  await setImmediate();
  assert.equal(el('connections-save').disabled, true, 'disabled forward mode may retain an empty URL');
  enable('onebot', true);
  const writesBeforeMissingURL = writes.length;
  el('connections-save').click();
  await setImmediate();
  assert.equal(writes.length, writesBeforeMissingURL, 're-enabling forward mode requires a valid URL');
  edit('onebot-url', 'ws://onebot.invalid:6199/');
  el('connections-save').click();
  await setImmediate();

  edit('onebot-mode', 'reverse');
  el('connections-save').click();
  await setImmediate();

  let releaseSave!: () => void;
  holdSave = new Promise<void>((resolve) => { releaseSave = resolve; });
  edit('onebot-mode', 'forward');
  const successesBeforeSwitch = successes.length;
  el('connections-save').click();
  await setImmediate();
  assert.equal(el('onebot-url').disabled, true, 'inputs must be frozen while the snapshot saves');
  assert.equal(saveSignal?.aborted, false);
  instanceState.select(second);
  assert.equal(el('onebot-enabled').disabled, true, 'stale checkbox must not change a different instance');
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
  root = dom.root();
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
  root = dom.root();
  cleanup = mountConnectionsPage(root as unknown as HTMLElement, api);
  await setImmediate();
  assert.equal(el('connections-error').hidden, false);
  assert.equal(el('connections-retry').hidden, false);
  assert.equal(el('connections-save').disabled, true);
  assert.equal(el('onebot-mode').disabled, true, 'failed loading must not overwrite saved settings with defaults');
  assert.equal(el('onebot-enabled').disabled, true);
  failLoad = false;
  el('connections-retry').click();
  await setImmediate();
  assert.equal(el('connections-error').hidden, true);
  assert.equal(el('onebot-mode').disabled, false);
  for (const adapter of ['onebot']) {
    edit(`${adapter}-mode`, 'reverse');
    edit(`${adapter}-inbound_token`, `synthetic-${adapter}-inbound`);
  }
  assert.ok(!root.innerHTML.includes('synthetic-onebot-inbound'));
  el('connections-save').click();
  await setImmediate();
  for (const prefix of prefixes) {
    assert.deepEqual(writes.at(-1)?.find((entry) => entry.key === `${prefix}_WS_INBOUND_TOKEN`), {
      key: `${prefix}_WS_INBOUND_TOKEN`, value: `synthetic-${prefix.toLowerCase()}-inbound`, isSecret: true,
    });
  }
  edit('onebot-mode', 'forward');
  assert.equal(el('onebot-inbound_token').disabled, true);
  assert.equal(el('onebot-inbound_token').value, 'synthetic-onebot-inbound', 'changing direction retains inbound credentials');
  el('connections-restore').click();
  enable('onebot', false);
  assert.equal(el('onebot-inbound_token').disabled, true);
  el('connections-restore').click();
  const writesBeforeInvalidInbound = writes.length;
  edit('onebot-inbound_token', 'synthetic secret');
  el('connections-save').click();
  await setImmediate();
  assert.equal(writes.length, writesBeforeInvalidInbound);
  assert.ok(!errors.at(-1)!.includes('synthetic secret'));
  edit('onebot-inbound_token', '');
  el('connections-save').click();
  await setImmediate();
  assert.deepEqual(writes.at(-1), [{ key: 'ONEBOT_WS_INBOUND_TOKEN', value: null, isSecret: true }]);
  assert.equal(values.find((setting) => setting.key === 'ONEBOT_WS_TOKEN')?.value, 'synthetic-onebot-token', 'clearing inbound token does not alter outbound credential');
  cleanup();

  instanceState.select(null);
  root = dom.root();
  cleanup = mountConnectionsPage(root as unknown as HTMLElement, api);
  assert.match(root.innerHTML, /请选择实例/);
  cleanup();
  console.log('PASS: connection modes, copy URLs, atomic drafts, validation, token handling, retry and instance isolation');
} finally {
  dom.cleanup();
}
