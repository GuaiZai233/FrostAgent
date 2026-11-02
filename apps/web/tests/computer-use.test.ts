import assert from 'node:assert/strict';
import {
  createComputerUseAPI,
  ComputerAPIError,
  type ComputerConfigUpdate,
  type ComputerSnapshot,
} from '../src/api/computer-use';
import { instanceState } from '../src/instance-state';
import { mountComputerUsePage } from '../src/pages/computer-use';
import {
  parseProjectEnvironment,
  taskStateLabel,
  validatedPreview,
  withTokenUpdate,
} from '../src/pages/computer-use-model';

Object.assign(globalThis, {
  window: Object.assign(new EventTarget(), {
    location: { origin: 'http://localhost' },
  }),
  document: { querySelectorAll: () => [] },
  localStorage: { getItem: () => 'synthetic-control-token' },
});
const calls: { url: string; init?: RequestInit }[] = [];
globalThis.fetch = async (url, init) => {
  calls.push({ url: String(url), init });
  return new Response('{}', { status: 200 });
};
const select = (id: string) =>
  instanceState.select({ id, name: id, enabled: true, created_at: '' });
select('synthetic-first');
const oldAPI = createComputerUseAPI();
select('synthetic-second');
await assert.rejects(oldAPI.snapshot());
assert.equal(calls.length, 0, 'stale page sent an instance request');
const api = createComputerUseAPI();
await api.snapshot();
assert.equal(calls[0].url, '/instances/synthetic-second/api/computer-use');
assert.equal(calls[0].init?.cache, 'no-store');
assert.equal(calls[0].init?.redirect, 'error');
assert.equal(
  new Headers(calls[0].init?.headers).get('Authorization'),
  'Bearer synthetic-control-token',
);
await api.resume('synthetic/task', 'new instruction', true);
assert.equal(
  calls[1].url,
  '/instances/synthetic-second/api/computer-use/tasks/synthetic%2Ftask/resume',
);
assert.deepEqual(JSON.parse(String(calls[1].init?.body)), {
  instruction: 'new instruction',
  acknowledge_unknown: true,
});
await api.reconcile('synthetic/task');
assert.equal(calls[2].init?.method, 'POST');
assert.ok(calls[2].url.endsWith('/synthetic%2Ftask/reconcile'));
await api.takeover();
assert.ok(calls[3].url.endsWith('/takeover'));
await api.resumeDesktop();
assert.ok(calls[4].url.endsWith('/desktop/resume'));

let resolveFetch: ((value: Response) => void) | undefined;
globalThis.fetch = () =>
  new Promise((resolve) => {
    resolveFetch = resolve;
  });
const pending = api.snapshot();
select('synthetic-third');
resolveFetch!(new Response('{}', { status: 200 }));
await assert.rejects(pending, 'late response from prior instance was accepted');

const currentAPI = createComputerUseAPI();
let contentCalls = 0;
globalThis.fetch = async () => {
  contentCalls++;
  return new Response('', { status: 410 });
};
await assert.rejects(
  currentAPI.content('synthetic-task'),
  (error) =>
    error instanceof ComputerAPIError &&
    error.status === 410 &&
    error.message.includes('不会重新执行'),
);
assert.equal(contentCalls, 1, 'expired content retried an operation');
currentAPI.dispose();
await assert.rejects(currentAPI.start('synthetic-project', 'instruction'));
assert.equal(contentCalls, 1, 'unmounted API dispatched an operation');

const config: ComputerConfigUpdate = {
  enabled: true,
  bridge_url: 'https://192.168.50.20:9443',
  ca_pem: '',
  max_concurrent: 2,
  max_seconds: 300,
  max_model_rounds: 10,
  max_tool_calls: 20,
  audit_retention_days: 7,
};
assert.equal(
  'token' in withTokenUpdate({ ...config, token: 'old-secret' }, '', false),
  false,
);
assert.equal(
  withTokenUpdate(config, ' synthetic-bridge-token ', false).token,
  'synthetic-bridge-token',
);
assert.equal(withTokenUpdate(config, 'ignored', true).token, '');
assert.equal(
  'adapter_token' in
    withTokenUpdate({ ...config, adapter_token: 'old-secret' }, '', false),
  false,
);
assert.equal(
  withTokenUpdate(config, '', false, ' synthetic-adapter-token ').adapter_token,
  'synthetic-adapter-token',
);
assert.equal(
  withTokenUpdate(config, '', false, 'ignored', true).adapter_token,
  '',
);
assert.deepEqual(
  Object.entries(parseProjectEnvironment('{"PATH":"C:\\\\Tools"}')),
  [['PATH', 'C:\\Tools']],
);
for (const value of [
  '[]',
  'null',
  '{"__proto__":"x"}',
  '{"constructor":"x"}',
  '{"PATH":4}',
  '{"BAD=KEY":"x"}',
  '{"PATH":"\\u0000"}',
])
  assert.throws(() => parseProjectEnvironment(value));
assert.equal(taskStateLabel('__proto__'), '__proto__');
assert.throws(() => validatedPreview({ expires_at: 'invalid' }));
assert.throws(() => validatedPreview({ expires_at: '2020-01-01T00:00:00Z' }));
assert.throws(() =>
  validatedPreview({
    png: 'javascript:alert(1)',
    expires_at: '2099-01-01T00:00:00Z',
  }),
);
assert.throws(() =>
  validatedPreview({
    png: 'iVBORw0KGgo" onerror="alert(1)',
    expires_at: '2099-01-01T00:00:00Z',
  }),
);
assert.equal(
  validatedPreview({
    text: '<script>literal</script>',
    expires_at: '2099-01-01T00:00:00Z',
  }).text,
  '<script>literal</script>',
);

class ElementMock {
  innerHTML = '';
  textContent = '';
  disabled = false;
  required = false;
  checked = false;
  hidden = false;
  readOnly = false;
  open = false;
  value = '';
  src = '';
  dataset: Record<string, string> = {};
  classList = { toggle: () => {} };
  private children = new Map<string, ElementMock>();
  private listeners = new Map<string, EventListener>();
  querySelector(selector: string): ElementMock {
    if (!this.children.has(selector))
      this.children.set(selector, new ElementMock());
    return this.children.get(selector)!;
  }
  querySelectorAll() {
    return [];
  }
  closest() {
    return this;
  }
  addEventListener(
    type: string,
    listener: EventListener,
    options?: AddEventListenerOptions,
  ) {
    this.listeners.set(type, listener);
    options?.signal?.addEventListener('abort', () =>
      this.listeners.delete(type),
    );
  }
  emit(type: string, target: ElementMock = this) {
    this.listeners.get(type)?.({
      target,
      preventDefault() {},
    } as unknown as Event);
  }
  reportValidity() {
    return true;
  }
  removeAttribute(name: string) {
    if (name === 'src') this.src = '';
  }
  focus() {}
  reset() {
    this.value = '';
  }
}
const snapshot: ComputerSnapshot = {
  config: {
    ...config,
    token_configured: true,
    adapter_token_configured: false,
  },
  model: '<model>',
  disclosure: '<script>disclosure</script>',
  desktop_paused: false,
  desktop_confirmed: false,
  desktop_generation: 0,
  projects: [
    {
      id: 'synthetic-project',
      path: '<img src=x onerror=alert(1)>',
      remote: '<script>remote</script>',
      baseline: 'main',
      shell: 'synthetic-shell',
      environment: {},
      archived: false,
    },
  ],
  workspaces: [
    {
      task_id: 'synthetic-task',
      project_id: 'synthetic-project',
      path: '<iframe src=x>',
      branch: '<script>branch</script>',
      shell: 'synthetic-shell',
      generation: 1,
      paused: true,
    },
  ],
  tasks: [
    {
      id: 'synthetic-task',
      project_id: 'synthetic-project',
      bridge_url: config.bridge_url,
      state: 'unknown',
      reason: '<script>reason</script>',
      generation: 1,
      model_rounds: 1,
      tool_calls: 2,
      gui_stopped: false,
      reconcile_required: true,
      created_at: '',
      updated_at: '',
    },
    {
      id: 'synthetic-restored-task',
      project_id: 'synthetic-project',
      bridge_url: '',
      state: 'paused',
      reason: 'restored_requires_rebind',
      generation: 3,
      model_rounds: 4,
      tool_calls: 5,
      gui_stopped: false,
      reconcile_required: true,
      created_at: '',
      updated_at: '',
    },
  ],
};
let snapshots = 0;
let mutations = 0;
let disposed = false;
let takeoverConfirmed = false;
let takeoverObservation:
  | Partial<
      Pick<
        ComputerSnapshot,
        'desktop_paused' | 'desktop_confirmed' | 'desktop_generation'
      >
    >
  | undefined;
let resumeArgs: unknown[] = [];
let startArgs: unknown[] = [];
const taskActions: string[] = [];
let resolvePause:
  | ((value: ComputerSnapshot['tasks'][number]) => void)
  | undefined;
const pageAPI = {
  ...createComputerUseAPI(),
  async snapshot() {
    snapshots++;
    return structuredClone({ ...snapshot, ...takeoverObservation });
  },
  async sync() {
    mutations++;
  },
  async takeover() {
    mutations++;
    snapshot.desktop_generation++;
    snapshot.desktop_paused = takeoverConfirmed;
    snapshot.desktop_confirmed = takeoverConfirmed;
    return {
      gui_stopped: takeoverConfirmed,
      desktop_paused: takeoverConfirmed,
      desktop_generation: snapshot.desktop_generation,
    };
  },
  async resumeDesktop() {
    mutations++;
    snapshot.desktop_generation++;
    snapshot.desktop_paused = false;
    snapshot.desktop_confirmed = true;
    return {
      gui_stopped: false,
      desktop_paused: false,
      desktop_generation: snapshot.desktop_generation,
    };
  },
  async start(...args: [string, string]) {
    mutations++;
    startArgs = args;
    return snapshot.tasks[0];
  },
  async resume(...args: [string, string, boolean?]) {
    mutations++;
    taskActions.push('resume');
    resumeArgs = args;
    return snapshot.tasks[0];
  },
  pause: () =>
    new Promise<ComputerSnapshot['tasks'][number]>((resolve) => {
      taskActions.push('pause');
      resolvePause = resolve;
    }),
  async reconcile() {
    taskActions.push('reconcile');
    return snapshot.tasks[0];
  },
  async content() {
    taskActions.push('content');
    return {
      text: '<script>raw output</script>',
      expires_at: new Date(Date.now() + 50).toISOString(),
    };
  },
  dispose() {
    disposed = true;
  },
};
const root = new ElementMock();
const cleanup = mountComputerUsePage(root as unknown as HTMLElement, pageAPI);
const settle = () => new Promise<void>((resolve) => setTimeout(resolve, 0));
await settle();
assert.equal(snapshots, 1);
assert.equal(mutations, 0, 'loading the workbench sent VM operations');
assert.equal(root.querySelector('#cu-token').value, '');
assert.equal(
  root.querySelector('#cu-disclosure').textContent,
  '<script>disclosure</script>',
);
assert.ok(root.querySelector('#cu-projects').innerHTML.includes('&lt;img'));
assert.ok(root.querySelector('#cu-projects').innerHTML.includes('&lt;iframe'));
assert.ok(
  root.querySelector('#cu-tasks').innerHTML.includes('&lt;script&gt;reason'),
);
assert.ok(
  root.querySelector('#cu-tasks').innerHTML.includes('尚未确认 GUI 输入停止'),
);
assert.match(
  root.querySelector('#cu-desktop-state').textContent,
  /缓存记录不能作为接管依据/,
);

const taskCards = root
  .querySelector('#cu-tasks')
  .innerHTML.split('<article class="cu-item">');
const restoredCard = taskCards.find((card) =>
  card.includes('synthetic-restored-task'),
)!;
assert.match(
  restoredCard,
  /仅供核对的恢复记录，配置桥\/项目后新建任务；不会重放旧操作/,
);
assert.match(restoredCard, /restored_requires_rebind/);
assert.match(restoredCard, /Generation 3/);
assert.match(restoredCard, /模型轮数 4/);
assert.match(restoredCard, /工具调用 5/);
assert.doesNotMatch(restoredCard, /data-cu-action=/);
const boundCard = taskCards.find((card) => card.includes('synthetic-task'))!;
for (const action of ['pause', 'resume', 'reconcile', 'content'])
  assert.ok(boundCard.includes('data-cu-action="' + action + '"'));

const restoredButton = new ElementMock();
for (const action of ['pause', 'resume', 'reconcile', 'content']) {
  restoredButton.dataset = {
    id: 'synthetic-restored-task',
    cuAction: action,
  };
  root.emit('click', restoredButton);
  await settle();
  assert.match(
    root.querySelector('#cu-feedback').textContent,
    /仅供核对的恢复记录/,
  );
}
assert.deepEqual(taskActions, [], 'restored record sent an old bridge request');
root.querySelector('#cu-task-project').value = 'synthetic-project';
root.querySelector('#cu-instruction').value = 'new independent task';
root.querySelector('#cu-task-form').emit('submit');
await settle();
assert.deepEqual(startArgs, ['synthetic-project', 'new independent task']);
assert.deepEqual(
  resumeArgs,
  [],
  'restored record silently resumed as a new task',
);

root.querySelector('#cu-takeover').emit('click');
await settle();
assert.match(
  root.querySelector('#cu-feedback').textContent,
  /尚未确认整机 GUI 输入停止并锁定，请勿接管/,
);
takeoverConfirmed = true;
root.querySelector('#cu-takeover').emit('click');
await settle();
assert.match(
  root.querySelector('#cu-feedback').textContent,
  /整机 GUI 输入停止并锁定，可以人工接管/,
);
for (const changed of [
  { desktop_paused: false },
  { desktop_confirmed: false },
  { desktop_generation: 999 },
]) {
  takeoverObservation = changed;
  root.querySelector('#cu-takeover').emit('click');
  await settle();
  assert.match(
    root.querySelector('#cu-feedback').textContent,
    /尚未确认整机 GUI 输入停止并锁定，请勿接管/,
  );
  assert.doesNotMatch(
    root.querySelector('#cu-feedback').textContent,
    /可以人工接管/,
  );
}
takeoverObservation = undefined;
root.querySelector('#cu-takeover').emit('click');
await settle();
assert.match(root.querySelector('#cu-feedback').textContent, /可以人工接管/);
snapshot.desktop_generation++;
snapshot.desktop_paused = false;
root.querySelector('#cu-refresh').emit('click');
await settle();
assert.doesNotMatch(
  root.querySelector('#cu-feedback').textContent,
  /可以人工接管/,
);
assert.match(
  root.querySelector('#cu-desktop-state').textContent,
  /整机桌面未锁定/,
);

const taskButton = new ElementMock();
taskButton.dataset = { id: 'synthetic-task', cuAction: 'resume' };
snapshot.tasks[0].state = 'paused';
snapshot.tasks[0].reconcile_required = false;
root.querySelector('#cu-refresh').emit('click');
await settle();
root.emit('click', taskButton);
assert.equal(root.querySelector('#cu-unknown-confirm').hidden, true);
assert.equal(root.querySelector('#cu-start').disabled, false);
root.querySelector('#cu-instruction').value = 'resume bound paused task';
root.querySelector('#cu-task-form').emit('submit');
await settle();
assert.deepEqual(resumeArgs, [
  'synthetic-task',
  'resume bound paused task',
  false,
]);

root.emit('click', taskButton);
root.querySelector('#cu-instruction').value = 'stale resume instruction';
snapshot.tasks[0].bridge_url = '';
root.querySelector('#cu-refresh').emit('click');
await settle();
assert.equal(root.querySelector('#cu-start').disabled, true);
const actionsBeforeDetachedSubmit = [...taskActions];
root.querySelector('#cu-task-form').emit('submit');
await settle();
assert.deepEqual(
  taskActions,
  actionsBeforeDetachedSubmit,
  'stale resume form sent a request for a detached task',
);
assert.match(
  root.querySelector('#cu-feedback').textContent,
  /仅供核对的恢复记录/,
);
snapshot.tasks[0].bridge_url = config.bridge_url;
snapshot.tasks[0].state = 'unknown';
snapshot.tasks[0].reconcile_required = true;
root.querySelector('#cu-refresh').emit('click');
await settle();
root.querySelector('#cu-cancel-resume').emit('click');

root.emit('click', taskButton);
assert.equal(root.querySelector('#cu-unknown-confirm').hidden, false);
assert.equal(root.querySelector('#cu-acknowledge-unknown').checked, false);
assert.equal(root.querySelector('#cu-acknowledge-unknown').required, true);
root.querySelector('#cu-acknowledge-unknown').checked = true;
root.querySelector('#cu-instruction').value = 'new explicit instruction';
root.querySelector('#cu-task-form').emit('submit');
await settle();
assert.deepEqual(resumeArgs, [
  'synthetic-task',
  'new explicit instruction',
  true,
]);
assert.equal(root.querySelector('#cu-instruction').value, '');

taskButton.dataset.cuAction = 'content';
root.emit('click', taskButton);
await settle();
assert.equal(
  root.querySelector('#cu-content-text').textContent,
  '<script>raw output</script>',
);
assert.equal(root.querySelector('#cu-content-text').innerHTML, '');
await new Promise<void>((resolve) => setTimeout(resolve, 75));
assert.equal(root.querySelector('#cu-content-text').textContent, '');
assert.match(
  root.querySelector('#cu-content-status').textContent,
  /过期.*不会重新执行/,
);

taskButton.dataset.cuAction = 'pause';
root.emit('click', taskButton);
await settle();
assert.equal(
  root.querySelector('#cu-takeover').disabled,
  false,
  'pending ordinary request disabled machine takeover',
);
const priorMutations = mutations;
root.querySelector('#cu-takeover').emit('click');
await settle();
assert.equal(
  mutations,
  priorMutations + 1,
  'machine takeover waited behind the pending task pause',
);
assert.match(
  root.querySelector('#cu-feedback').textContent,
  /整机 GUI 输入停止并锁定，可以人工接管/,
);
resolvePause!(snapshot.tasks[0]);
await settle();
assert.match(
  root.querySelector('#cu-feedback').textContent,
  /尚未确认 GUI 输入停止。请勿接管/,
);
root.emit('click', taskButton);
await settle();
const beforeUnmount = snapshots;
const priorFeedback = root.querySelector('#cu-feedback').textContent;
root.querySelector('#cu-token').value = 'synthetic-secret';
root.querySelector('#cu-adapter-token').value = 'synthetic-adapter-secret';
root.querySelector('#cu-instruction').value = 'sensitive instruction';
cleanup();
resolvePause!({ ...snapshot.tasks[0], gui_stopped: true });
await settle();
assert.equal(disposed, true);
assert.equal(snapshots, beforeUnmount, 'late pause reloaded unmounted page');
assert.equal(root.querySelector('#cu-feedback').textContent, priorFeedback);
assert.equal(root.querySelector('#cu-token').value, '');
assert.equal(root.querySelector('#cu-adapter-token').value, '');
assert.equal(root.querySelector('#cu-instruction').value, '');
assert.equal(root.querySelector('#cu-content-image').src, '');
console.log(
  'Computer Use API isolation, content privacy, explicit recovery and takeover checks passed',
);
