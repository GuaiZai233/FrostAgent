import assert from 'node:assert/strict';
import { create } from '@bufbuild/protobuf';
import { ModelBindingMode, ModelRouterConfigurationSchema, ModelWorkload, type ModelRouterConfiguration } from '@frostagent/proto';
import { openGroupModelConfig } from '../src/components/group-model-config';
import { bindingOptions, workloads } from '../src/utils/model-bindings';
import { toast } from '../src/components/toast';

class MockElement {
  innerHTML = '';
  textContent = '';
  disabled = false;
  value = '';
  dataset: Record<string, string> = {};
  style: Record<string, string> = {};
  private listeners = new Map<string, (event?: Event) => unknown>();
  private children = new Map<string, MockElement>();
  private selects: MockElement[] | undefined;
  addEventListener(event: string, listener: (event?: Event) => unknown) { this.listeners.set(event, listener); }
  click() { return this.listeners.get('click')?.(); }
  emit(event: string, payload: unknown) { return this.listeners.get(event)?.(payload as Event); }
  querySelector(selector: string): MockElement {
    if (!this.children.has(selector)) this.children.set(selector, new MockElement());
    return this.children.get(selector)!;
  }
  querySelectorAll(selector: string): MockElement[] {
    if (selector === '.dialog-close-btn') return [this.querySelector(selector), this.querySelector('.dialog-close-btn-header')];
    if (selector !== '.group-model-binding') return [];
    return this.selects ??= [...this.innerHTML.matchAll(/<select[^>]*data-workload="(\d+)"[^>]*>(.*?)<\/select>/gs)].map((match) => {
      const select = new MockElement();
      select.dataset.workload = match[1];
      select.innerHTML = match[2];
      select.value = /<option value="([^"]+)" selected/.exec(match[2])?.[1] || 'inherit';
      return select;
    });
  }
}

class MockDialog extends MockElement {
  closed = false;
  className = '';
  showModal() {}
  close() { this.closed = true; }
  remove() {}
  getBoundingClientRect() { return { top: 10, bottom: 110, left: 10, width: 200 }; }
}

const dialogs: MockDialog[] = [];
Object.assign(globalThis, {
  document: {
    body: { appendChild() {} },
    createElement: () => {
      const dialog = new MockDialog();
      dialogs.push(dialog);
      return dialog;
    },
  },
});
const errors: string[] = [];
const successes: string[] = [];
toast.error = (message) => { errors.push(message); };
toast.success = (message) => { successes.push(message); };

function fixture() {
  const active = create(ModelRouterConfigurationSchema, {
    version: 1, revision: 5n,
    endpoints: [{ id: 'endpoint', displayName: 'Endpoint <name>', enabled: true }],
    models: [{ id: 'model', displayName: 'Model <name>', endpointId: 'endpoint', upstreamModel: 'test-model', enabled: true }],
    globalBindings: [{ workload: ModelWorkload.DIALOGUE, mode: ModelBindingMode.MODEL, modelId: 'model' }],
    groupOverrides: [
      { platform: 'qq', groupId: '123', bindings: [{ workload: ModelWorkload.DIALOGUE, mode: ModelBindingMode.DISABLED }] },
      { platform: 'onebot', groupId: '456', bindings: [{ workload: ModelWorkload.VISION, mode: ModelBindingMode.DISABLED }] },
      { platform: 'telegram', groupId: '123', bindings: [{ workload: ModelWorkload.SUBAGENT, mode: ModelBindingMode.DISABLED }] },
    ],
  });
  const draft = structuredClone(active);
  draft.globalBindings = [];
  draft.groupOverrides[1].bindings = [];
  const calls: string[] = [];
  const saved: ModelRouterConfiguration[] = [];
  let refreshed = 0;
  const state = { loadError: '', saveError: '', publishError: '',
    loadGate: Promise.resolve(), saveGate: Promise.resolve(), publishGate: Promise.resolve() };
  const api = {
    async getModelRouterState() {
      calls.push('load');
      await state.loadGate;
      return { active, draft, loadError: state.loadError, hasDraftSecretChanges: true };
    },
    async saveModelRouterDraft(configuration: ModelRouterConfiguration) {
      calls.push('save');
      saved.push(structuredClone(configuration));
      await state.saveGate;
      return { success: !state.saveError, error: state.saveError, draft: configuration };
    },
    async publishModelRouter(expected: ModelRouterConfiguration) {
      calls.push('publish');
      assert.deepEqual(expected, saved.at(-1), 'group publication did not bind to its saved configuration');
      await state.publishGate;
      return { success: !state.publishError, error: state.publishError, active: saved.at(-1) };
    },
  } as unknown as Parameters<typeof openGroupModelConfig>[2];
  const open = (platform = 'onebot', groupId = '123') => {
    const cleanup = openGroupModelConfig(platform, groupId, api, () => { refreshed++; });
    const dialog = dialogs.at(-1)!;
    const body = dialog.querySelector('.dialog-body');
    const button = dialog.querySelector('#group-model-publish');
    button.disabled = true;
    return { cleanup, dialog, body, button };
  };
  return { active, saved, calls, state, open, get refreshed() { return refreshed; } };
}

async function flush() { for (let index = 0; index < 5; index++) await Promise.resolve(); }

{
  const f = fixture();
  const { dialog, body, button } = f.open('aiocqhttp');
  await flush();
  const selects = body.querySelectorAll('.group-model-binding');
  assert.equal(selects.length, 7);
  assert.equal(selects[0].value, 'disabled', 'QQ platform aliases must restore the existing group binding');
  assert.equal(selects[1].value, 'inherit');
  assert.match(body.innerHTML, /Model &lt;name&gt; · Endpoint &lt;name&gt; · test-model/);
  for (const workload of workloads) {
    const select = selects.find((item) => Number(item.dataset.workload) === workload.value)!;
    const globalOptions = bindingOptions(f.active, undefined, false, '继承实例默认', workload.value !== ModelWorkload.REFLECTION, workload.value !== ModelWorkload.DIALOGUE && workload.value !== ModelWorkload.EMBEDDING);
    assert.equal(/value="follow-dialogue"/.test(select.innerHTML), /value="follow-dialogue"/.test(globalOptions));
    assert.equal(/value="disabled"/.test(select.innerHTML), workload.value===ModelWorkload.EMBEDDING ? false : /value="disabled"/.test(globalOptions));
    assert.match(select.innerHTML, /value="model:model"/);
  }
  selects[0].value = 'model:model';
  selects[1].value = 'follow-dialogue';
  selects[2].value = 'disabled';
  await button.click();
  assert.deepEqual(f.calls, ['load', 'save', 'publish']);
  assert.deepEqual(f.saved[0].globalBindings, f.active.globalBindings, 'unrelated draft defaults were published');
  assert.deepEqual(f.saved[0].groupOverrides.slice(1), f.active.groupOverrides.slice(1), 'other group routes changed');
  assert.deepEqual(f.saved[0].models, f.active.models);
  assert.deepEqual(f.saved[0].endpoints, f.active.endpoints);
  assert.equal(f.saved[0].revision, 5n);
  assert.equal(f.saved[0].groupOverrides.length, 3, 'platform alias created a duplicate group');
  assert.equal(f.saved[0].groupOverrides[0].platform, 'qq');
  assert.equal(f.saved[0].groupOverrides[0].bindings[0].modelId, 'model');
  assert.equal(f.saved[0].groupOverrides[0].bindings[1].mode, ModelBindingMode.FOLLOW_DIALOGUE);
  assert.equal(dialog.closed, true);
  assert.equal(f.refreshed, 1);
  assert.equal(successes.at(-1), '群级模型配置已发布');
}

{
  const f = fixture();
  const { dialog, body, button } = f.open();
  await flush();
  body.querySelectorAll('.group-model-binding').forEach((select) => { select.value = 'inherit'; });
  f.state.publishError = 'publish unavailable';
  await button.click();
  assert.equal(dialog.closed, false);
  assert.equal(button.disabled, false);
  assert.ok(dialog.querySelectorAll('.dialog-close-btn').every((item) => !item.disabled), 'failed publication left dismissal disabled');
  assert.equal(f.refreshed, 0);
  assert.match(errors.at(-1) || '', /publish unavailable/);
  f.state.publishError = '';
  await button.click();
  assert.deepEqual(f.saved[1].groupOverrides, f.active.groupOverrides.slice(1), 'retry removed an unrelated group');
  assert.equal(f.refreshed, 1);
}

{
  const f = fixture();
  const { button } = f.open('telegram', '789');
  await flush();
  f.state.saveError = 'stale revision';
  await button.click();
  assert.deepEqual(f.calls, ['load', 'save']);
  assert.equal(f.refreshed, 0);
  assert.equal(button.disabled, false);
  assert.match(errors.at(-1) || '', /stale revision/);
}

{
  const f = fixture();
  const { body, button } = f.open('telegram', '789');
  await flush();
  body.querySelectorAll('.group-model-binding')[0].value = 'model:model';
  await button.click();
  assert.equal(f.saved[0].groupOverrides.at(-1)?.platform, 'telegram');
  assert.equal(f.saved[0].groupOverrides.at(-1)?.groupId, '789');
  assert.deepEqual(f.saved[0].groupOverrides.slice(0, 3), f.active.groupOverrides);
}

{
  const f = fixture();
  f.state.loadError = 'configuration unavailable';
  const { button, cleanup } = f.open();
  await flush();
  assert.equal(button.disabled, true);
  assert.deepEqual(f.calls, ['load']);
  assert.match(errors.at(-1) || '', /configuration unavailable/);
  cleanup();
}

{
  const f = fixture();
  let release!: () => void;
  f.state.loadGate = new Promise((resolve) => { release = resolve; });
  const { cleanup, body, dialog } = f.open();
  cleanup();
  release();
  await flush();
  assert.equal(dialog.closed, true);
  assert.doesNotMatch(body.innerHTML, /group-model-binding/);
}

{
  const f = fixture();
  const { button, cleanup } = f.open();
  await flush();
  let release!: () => void;
  f.state.saveGate = new Promise((resolve) => { release = resolve; });
  const pending = button.click();
  button.click();
  await flush();
  assert.deepEqual(f.calls, ['load', 'save'], 'duplicate clicks started another save');
  cleanup();
  release();
  await pending;
  assert.deepEqual(f.calls, ['load', 'save'], 'closed dialog published a stale edit');
  assert.equal(f.refreshed, 0);
}

{
  const f = fixture();
  const first = f.open();
  await flush();
  let release!: () => void;
  f.state.saveGate = new Promise((resolve) => { release = resolve; });
  first.body.querySelectorAll('.group-model-binding')[0].value = 'model:model';
  const firstSave = first.button.click();
  await flush();
  first.cleanup();
  const second = f.open('telegram', '789');
  await flush();
  second.body.querySelectorAll('.group-model-binding')[0].value = 'model:model';
  f.state.saveGate = Promise.resolve();
  const secondSave = second.button.click();
  await flush();
  assert.deepEqual(f.calls, ['load', 'save', 'load'], 'reopened dialog overtook an unfinished save');
  release();
  await firstSave;
  await secondSave;
  assert.deepEqual(f.calls, ['load', 'save', 'load', 'save', 'publish']);
  assert.deepEqual(f.saved[1].groupOverrides[0], f.active.groupOverrides[0], 'closed dialog edit leaked into the next publication');
  assert.equal(f.saved[1].groupOverrides.at(-1)?.groupId, '789');
  assert.equal(f.refreshed, 1);
}

for (const failure of [false, true]) {
  const f = fixture();
  const { dialog, button } = f.open();
  await flush();
  let release!: () => void;
  f.state.publishGate = new Promise((resolve) => { release = resolve; });
  f.state.publishError = failure ? 'publication rejected' : '';
  const pending = button.click();
  await flush();
  assert.deepEqual(f.calls, ['load', 'save', 'publish']);
  assert.equal(button.textContent, '发布中...');
  for (const dismissButton of dialog.querySelectorAll('.dialog-close-btn')) {
    assert.equal(dismissButton.disabled, true);
    dismissButton.click();
    assert.equal(dialog.closed, false, 'a button dismissed the in-flight publication');
  }
  const escape = new Event('cancel', { cancelable: true });
  dialog.emit('cancel', escape);
  assert.equal(escape.defaultPrevented, true);
  assert.equal(dialog.closed, false, 'Escape dismissed the in-flight publication');
  dialog.emit('click', { target: dialog, clientX: 0, clientY: 0 });
  assert.equal(dialog.closed, false, 'backdrop dismissed the in-flight publication');
  release();
  await pending;
  if (failure) {
    assert.equal(dialog.closed, false);
    assert.equal(f.refreshed, 0);
    assert.match(errors.at(-1) || '', /publication rejected/);
    assert.ok(dialog.querySelectorAll('.dialog-close-btn').every((item) => !item.disabled));
    dialog.querySelector('.dialog-close-btn').click();
    assert.equal(dialog.closed, true, 'failed publication could not be dismissed');
  } else {
    assert.equal(dialog.closed, true);
    assert.equal(f.refreshed, 1, 'publication success did not refresh the context');
    assert.equal(successes.at(-1), '群级模型配置已发布');
    const next = f.open('telegram', '789');
    await flush();
    assert.equal(next.button.disabled, false, 'completed publication blocked a new dialog');
    next.cleanup();
  }
}

{
  const f = fixture();
  const { dialog, button, cleanup } = f.open();
  await flush();
  let release!: () => void;
  f.state.publishGate = new Promise((resolve) => { release = resolve; });
  const pending = button.click();
  await flush();
  const successCount = successes.length;
  cleanup();
  assert.equal(dialog.closed, true, 'page teardown left a busy dialog mounted');
  release();
  await pending;
  assert.equal(f.refreshed, 0, 'unmounted page refreshed through an old publication');
  assert.equal(successes.length, successCount);
}

process.stdout.write('PASS: inline group model options, isolated publication, retry, and lifecycle guards\n');
