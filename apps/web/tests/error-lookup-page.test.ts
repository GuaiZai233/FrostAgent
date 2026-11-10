import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { setImmediate } from 'node:timers/promises';
import { create, type MessageInitShape } from '@bufbuild/protobuf';
import {
  LogEntrySchema,
  ListLogsResponseSchema,
  LogLevel,
} from '@frostagent/proto';
import { createInstanceAPI } from '../src/api/client';
import { mountLogsPage } from '../src/pages/logs';
import { instanceState } from '../src/instance-state';
import { toast } from '../src/components/toast';

const { JSDOM } = createRequire(resolve('package.json'))('jsdom');
const dom = new JSDOM('<body><main></main></body>', {
  url: 'http://panel.example/',
});
const originals = new Map(
  ['window', 'document', 'Event'].map((key) => [
    key,
    Object.getOwnPropertyDescriptor(globalThis, key),
  ]),
);
for (const [key, value] of Object.entries({
  window: dom.window,
  document: dom.window.document,
  Event: dom.window.Event,
})) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    writable: true,
    value,
  });
}
dom.window.HTMLDialogElement.prototype.showModal = function () {
  this.open = true;
};
dom.window.HTMLDialogElement.prototype.close = function () {
  this.open = false;
};
const root = document.querySelector<HTMLElement>('main')!;
const uuid = '00000000-0000-4000-8000-000000000001';
const target = create(LogEntrySchema, {
  id: 'test:1',
  errorUuid: uuid,
  level: LogLevel.ERROR,
  source: 'SYSTEM',
  timestamp: '2026-01-01T00:00:00Z',
  summary: 'exact error <script>diagnostic</script>',
});
const response = (entries: MessageInitShape<typeof LogEntrySchema>[]) =>
  create(ListLogsResponseSchema, {
    entries,
    pagination: { total: entries.length },
  });
const calls: unknown[][] = [];
let pending: ((value: ReturnType<typeof response>) => void) | undefined;
let deferLookup = false;
let failLookup = false;
const api = {
  getOverview: async () => ({ botName: 'Test' }),
  listLogs: async (...args: unknown[]) => {
    calls.push(args);
    if (!args[4]) return response([]);
    if (deferLookup)
      return new Promise<ReturnType<typeof response>>((resolve) => {
        pending = resolve;
      });
    if (failLookup) throw new Error('未找到错误日志');
    return response([target]);
  },
} as unknown as ReturnType<typeof createInstanceAPI>;
const oldToast = toast.error;
const errors: string[] = [];
toast.error = (value) => {
  errors.push(value);
};
const flush = async () => {
  for (let i = 0; i < 5; i++) await setImmediate();
};
function lookup(value: string) {
  root.querySelector<HTMLInputElement>('#logs-error-uuid-input')!.value = value;
  root
    .querySelector('form')!
    .dispatchEvent(new dom.window.Event('submit', { cancelable: true }));
}
let cleanup = () => {};
try {
  instanceState.select({
    id: 'test',
    name: 'test',
    enabled: true,
    created_at: '',
  });
  instanceState.logSource = 'instance';
  cleanup = mountLogsPage(root, api);
  await flush();
  assert.ok(
    root.innerHTML.indexOf('logs-error-lookup-form') <
      root.innerHTML.indexOf('logs-source-select'),
  );
  lookup('invalid');
  await flush();
  assert.equal(calls.length, 1, 'invalid UUID must not reach API');
  lookup(uuid.toUpperCase());
  await flush();
  assert.deepEqual(calls.at(-1)?.slice(1), [
    '',
    LogLevel.UNSPECIFIED,
    '',
    uuid,
  ]);
  assert.equal(
    root.querySelector('tr.bg-muted')?.getAttribute('data-id'),
    target.id,
  );
  assert.match(
    root.querySelector('#log-detail-content')!.textContent!,
    new RegExp(uuid),
  );
  assert.match(document.querySelector('dialog')!.textContent!, /exact error/);
  assert.equal(
    document.querySelector('dialog script'),
    null,
    'log content must remain escaped',
  );
  document.querySelector('dialog')!.remove();

  failLookup = true;
  lookup(uuid);
  await flush();
  assert.equal(document.querySelector('dialog'), null);
  assert.ok(errors.some((error) => error.includes('未找到')));

  failLookup = false;
  deferLookup = true;
  lookup(uuid);
  await flush();
  const sourceSelect = root.querySelector<HTMLSelectElement>(
    '#logs-source-select',
  )!;
  sourceSelect.value = 'control-plane';
  sourceSelect.dispatchEvent(new dom.window.Event('change'));
  pending!(response([target]));
  await flush();
  assert.equal(
    document.querySelector('dialog'),
    null,
    'stale lookup must not open after source switch',
  );
  assert.equal(root.querySelector('tr[data-id]'), null);

  lookup(uuid);
  await flush();
  cleanup();
  pending!(response([target]));
  await flush();
  assert.equal(
    document.querySelector('dialog'),
    null,
    'unmounted page must not open lookup',
  );
} finally {
  cleanup();
  toast.error = oldToast;
  instanceState.select(null);
  for (const [key, descriptor] of originals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor);
    else Reflect.deleteProperty(globalThis, key);
  }
  dom.window.close();
}
console.log(
  'PASS: error UUID lookup validates, locates, opens details, and rejects stale results',
);
