import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { setImmediate } from 'node:timers/promises';
import { create } from '@bufbuild/protobuf';
import { LogEntrySchema, LogLevel } from '@frostagent/proto';
import { mountLogsPage } from '../src/pages/logs';
import { copyToClipboard } from '../src/utils/clipboard';
import { formatFullLog } from '../src/utils/formatters';
import { instanceState } from '../src/instance-state';
import { toast } from '../src/components/toast';

const { JSDOM } = createRequire(resolve('package.json'))('jsdom');
const dom = new JSDOM('<body><main></main></body>', {
  url: 'http://panel.example/',
});
const globals = new Map(
  ['window', 'document', 'navigator', 'Event'].map((key) => [
    key,
    Object.getOwnPropertyDescriptor(globalThis, key),
  ]),
);
for (const [key, value] of Object.entries({
  window: dom.window,
  document: dom.window.document,
  navigator: dom.window.navigator,
  Event: dom.window.Event,
})) {
  Object.defineProperty(globalThis, key, { configurable: true, value });
}
dom.window.HTMLDialogElement.prototype.showModal = function () {
  this.open = true;
};
dom.window.HTMLDialogElement.prototype.close = function () {
  this.open = false;
};
const flush = async () => {
  for (let i = 0; i < 5; i++) await setImmediate();
};
const oldSuccess = toast.success;
const oldError = toast.error;
const successes: string[] = [];
const errors: string[] = [];
toast.success = (message) => {
  successes.push(message);
};
toast.error = (message) => {
  errors.push(message);
};
const summary =
  'BEGIN\n' +
  'long diagnostic '.repeat(500) +
  '\n' +
  'x'.repeat(4000) +
  '\n<script>escaped</script>\nEND';
const entry = create(LogEntrySchema, {
  id: 'synthetic:1',
  instanceId: 'synthetic',
  instanceName: 'Test Instance',
  timestamp: '2026-01-01T12:34:56.123Z',
  level: LogLevel.ERROR,
  source: 'HTTP',
  summary,
  requestBody: 'synthetic request',
  responseBody: 'synthetic response',
  traceId: 'private:synthetic-user',
  errorUuid: '00000000-0000-4000-8000-000000000001',
});
let copied = '';
let execResult = true;
let execThrows = false;
let expectedHost: Element | null = null;
let execCalls = 0;
Object.defineProperty(document, 'execCommand', {
  value: (command: string) => {
    execCalls++;
    assert.equal(command, 'copy');
    const input = document.activeElement as HTMLTextAreaElement;
    assert.equal(input.tagName, 'TEXTAREA', 'fallback must focus its textarea');
    assert.equal(
      input.closest('dialog'),
      expectedHost,
      'selection must be inside the active dialog',
    );
    assert.equal(input.selectionStart, 0);
    assert.equal(input.selectionEnd, input.value.length);
    copied = input.value;
    if (execThrows) throw new Error('synthetic clipboard failure');
    return execResult;
  },
});
let stop = () => {};
try {
  instanceState.select(null);
  const root = document.querySelector<HTMLElement>('main')!;
  stop = mountLogsPage(root, {
    listLogs: async () => ({ entries: [entry], pagination: { total: 1 } }),
  } as unknown as NonNullable<Parameters<typeof mountLogsPage>[1]>);
  await flush();
  root
    .querySelector<HTMLButtonElement>('[data-action="view-summary"]')!
    .click();
  const dialog = document.querySelector('dialog')!;
  expectedHost = dialog;
  const content = dialog.querySelector<HTMLElement>('.log-summary-content')!;
  assert.equal(
    content.textContent,
    summary,
    'display must retain the entire long log',
  );
  assert.equal(content.tabIndex, 0);
  assert.equal(content.querySelector('script'), null);
  const button = dialog.querySelector<HTMLButtonElement>(
    '#log-summary-copy-btn',
  )!;
  assert.equal(button.textContent?.trim(), '复制完整日志');
  button.focus();
  button.click();
  await flush();
  assert.ok(copied.includes(summary), 'copied log must retain the entire body');
  for (const value of [
    entry.timestamp,
    entry.id,
    entry.instanceId,
    entry.instanceName,
    entry.source,
    entry.traceId,
    entry.errorUuid,
    entry.requestBody,
    entry.responseBody,
  ])
    assert.ok(copied.includes(value), 'missing copied field: ' + value);
  assert.equal(
    document.activeElement,
    button,
    'fallback must restore button focus',
  );
  assert.equal(document.querySelector('textarea'), null);
  assert.equal(successes.at(-1), '已复制完整日志');

  execResult = false;
  button.click();
  await flush();
  assert.equal(
    successes.length,
    1,
    'failed copy must not show a success toast',
  );
  assert.equal(errors.at(-1), '复制失败，请手动复制');
  execThrows = true;
  assert.equal(await copyToClipboard('failed'), false);
  assert.equal(
    document.querySelector('textarea'),
    null,
    'failed copy must remove the textarea',
  );
  assert.equal(document.activeElement, button);
  execThrows = false;
  execResult = true;

  const nested = document.createElement('dialog');
  nested.innerHTML = '<button>Nested copy</button>';
  dialog.appendChild(nested);
  nested.showModal();
  expectedHost = nested;
  const nestedButton = nested.querySelector('button')!;
  nestedButton.focus();
  assert.equal(await copyToClipboard('nested'), true);
  assert.equal(copied, 'nested');
  assert.equal(document.activeElement, nestedButton);
  nested.remove();

  Object.defineProperty(window, 'isSecureContext', {
    configurable: true,
    value: true,
  });
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: {
      writeText: async (text: string) => {
        copied = text;
      },
    },
  });
  const callsBeforeSecureCopy = execCalls;
  assert.equal(await copyToClipboard('secure'), true);
  assert.equal(copied, 'secure');
  assert.equal(
    execCalls,
    callsBeforeSecureCopy,
    'secure copy must use Clipboard API',
  );
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: {
      writeText: async () => {
        throw new Error('denied');
      },
    },
  });
  expectedHost = dialog;
  button.focus();
  assert.equal(await copyToClipboard('fallback after denial'), true);
  assert.equal(copied, 'fallback after denial');
  dialog.remove();
  expectedHost = null;
  assert.equal(await copyToClipboard('outside dialog'), true);
  assert.equal(copied, 'outside dialog');

  const request = create(LogEntrySchema, { requestBody: 'request only' });
  const response = create(LogEntrySchema, { responseBody: 'response only' });
  assert.ok(formatFullLog(request).endsWith('request only'));
  assert.ok(formatFullLog(response).endsWith('response only'));
  assert.equal(
    formatFullLog(
      create(LogEntrySchema, {
        summary: 'same content',
        requestBody: 'same content',
      }),
    ).split('same content').length,
    2,
    'identical body must not be duplicated',
  );
} finally {
  stop();
  document.querySelectorAll('dialog').forEach((dialog) => dialog.remove());
  toast.success = oldSuccess;
  toast.error = oldError;
  instanceState.select(null);
  for (const [key, descriptor] of globals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor);
    else Reflect.deleteProperty(globalThis, key);
  }
  dom.window.close();
}
console.log(
  'PASS: long logs copy complete details, modal fallback selects text, and failures stay visible',
);
