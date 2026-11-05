import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { setImmediate } from 'node:timers/promises';
import { mountOverviewPage } from '../src/pages/overview';
import { mountPromptPage } from '../src/pages/prompt';
import { mountMemoryPage } from '../src/pages/memory';
import { mountStickersPage } from '../src/pages/stickers';
import { mountChatPage } from '../src/pages/chat';
import { mountVersionControl } from '../src/components/version-panel';
import { instanceState } from '../src/instance-state';

const { JSDOM } = createRequire(resolve('package.json'))('jsdom');
const dom = new JSDOM('<body><main></main></body>', { url: 'http://panel.example/' });
const originals = new Map(['window', 'document', 'Event', 'fetch', 'localStorage', 'WebSocket'].map((key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
for (const [key, value] of Object.entries({ window: dom.window, document: dom.window.document, Event: dom.window.Event, localStorage: dom.window.localStorage })) {
  Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
}
const root = document.querySelector<HTMLElement>('main')!;
dom.window.HTMLDialogElement.prototype.showModal = function () { this.open = true; };
dom.window.HTMLDialogElement.prototype.close = function () { this.open = false; };
const calls: string[] = [];
let releaseCheck: (() => void) | undefined;
let pendingCheck = false;
let failCheck = false;
let checkSignal: AbortSignal | undefined;
let releaseVersion: (() => void) | undefined;
let pendingVersion = false;
let sockets = 0;
Object.defineProperty(globalThis, 'WebSocket', { configurable: true, value: class { constructor() { sockets++; } } });
localStorage.setItem('frostagent_control_token', 'synthetic-control-token');
globalThis.fetch = async (input, init) => {
  const url = new URL(input instanceof Request ? input.url : String(input), dom.window.location.origin);
  assert.equal(url.origin, dom.window.location.origin, 'browser must only call the control plane');
  assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer synthetic-control-token');
  calls.push(url.pathname);
  if (url.pathname === '/api/version') {
    if (pendingVersion) await new Promise<void>((resolve) => { releaseVersion = resolve; });
    return Response.json({ display: '123abcd', version: '', commit: '123abcd' + 'a'.repeat(33) });
  }
  if (url.pathname === '/api/update-check') {
    checkSignal = init?.signal as AbortSignal;
    if (pendingCheck) await new Promise<void>((resolve) => { releaseCheck = resolve; });
    if (failCheck) return new Response('unavailable', { status: 503 });
    return Response.json({
      current: { display: '123abcd', version: '', commit: '123abcd' + 'a'.repeat(33) },
      stable: { status: 'no-release', error: '暂无正式 Release' },
      latest: { status: 'different', value: 'b'.repeat(40), url: 'javascript:alert(1)' },
      checked_at: '2026-01-01T00:00:00Z',
    });
  }
  throw new Error(`Unexpected request without instance: ${url.pathname}`);
};
const flush = async () => { for (let i = 0; i < 8; i++) await setImmediate(); };

try {
  instanceState.select(null);
  for (const [mount, selectors] of [
    [mountPromptPage, ['#prompt-session-list', '#prompt-inspector-mount', '#prompt-session-search-input']],
    [mountMemoryPage, ['#memory-table-body', '#memory-stats-container', '#memory-search-input']],
    [mountStickersPage, ['#sticker-grid', '#sticker-stats-container', '#sticker-search-input']],
    [mountChatPage, ['#chat-messages-container', '#chat-input-textarea', '#btn-send-message']],
  ] as const) {
    root.innerHTML = '';
    const cleanup = mount(root);
    await flush();
    for (const selector of selectors) assert.ok(root.querySelector(selector), selector);
    assert.equal(root.querySelector('.spinner'), null, 'empty framework must not keep loading');
    assert.equal(root.querySelector('[href*="/api/instances/"]'), null, 'empty framework must not offer invalid downloads');
    if (mount === mountPromptPage) {
      assert.match(root.querySelector('#prompt-session-list')!.textContent || '', /暂无/);
      assert.equal(root.querySelector<HTMLButtonElement>('#prompt-sessions-refresh-btn')!.disabled, true);
      const search = root.querySelector<HTMLInputElement>('#prompt-session-search-input')!;
      search.value = 'synthetic';
      search.dispatchEvent(new dom.window.Event('input'));
      root.querySelector<HTMLButtonElement>('[data-filter="all-groups"]')!.click();
      root.querySelector<HTMLButtonElement>('#prompt-toggle-editor-btn')!.click();
      root.querySelector<HTMLTextAreaElement>('#prompt-raw-input')!.value = '{"messages":[{"role":"user","content":"local-only"}]}';
      root.querySelector<HTMLButtonElement>('#prompt-apply-input-btn')!.click();
      assert.match(root.querySelector('#prompt-inspector-mount')!.textContent || '', /local-only/);
    } else {
      for (const control of root.querySelectorAll<HTMLInputElement>('input, button, select, textarea')) assert.equal(control.disabled, true);
    }
    await flush();
    assert.deepEqual(calls, [], 'empty data pages must not fetch');
    assert.equal(sockets, 0);
    cleanup();
    instanceState.select(null);
  }

  let cleanup = mountOverviewPage(root);
  await flush();
  assert.deepEqual(calls, ['/api/version'], 'overview must not fetch instance data or check GitHub automatically');
  assert.match(root.textContent || '', /请选择实例/);
  assert.match(root.querySelector('#build-version')!.textContent || '', /123abcd/);
  const versionBadge = root.querySelector('#build-version')!.parentElement!;
  assert.equal(versionBadge.nextElementSibling?.id, 'check-updates', 'check button must sit immediately to the right of the version badge');
  assert.ok(versionBadge.querySelector('svg'), 'version badge must retain its code icon');
  const checkButton = root.querySelector<HTMLButtonElement>('#check-updates')!;
  assert.equal(checkButton.textContent?.trim(), '检查更新', 'update button must have a visible text label');
  assert.ok(checkButton.classList.contains('btn-outline'), 'update button must have its own outlined box');
  assert.ok(!checkButton.classList.contains('btn-icon-sm'), 'update button must not be an icon-only control');
  assert.ok(checkButton.querySelector('svg'), 'update button must retain a static update icon');
  assert.equal(root.querySelector('#overview-version .card'), null, 'version must not have a standalone card');
  assert.equal(root.querySelector('#update-results'), null, 'results must not be embedded in the overview');
  assert.doesNotMatch(root.textContent || '', /仅查询 GitHub 仓库|检查结果缓存/);
  pendingCheck = true;
  root.querySelector<HTMLButtonElement>('#check-updates')!.click();
  root.querySelector<HTMLButtonElement>('#check-updates')!.click();
  await flush();
  assert.equal(document.querySelectorAll('dialog').length, 1, 'duplicate clicks must not open multiple dialogs');
  assert.match(document.querySelector('dialog')!.textContent || '', /检查更新.*正在检查 GitHub/s);
  assert.doesNotMatch(document.querySelector('dialog')!.textContent || '', /仅查询 GitHub 仓库|检查结果缓存/);
  assert.equal(calls.filter((path) => path === '/api/update-check').length, 1, 'duplicate clicks must coalesce');
  releaseCheck!();
  await flush();
  assert.match(document.querySelector('dialog #update-results')!.textContent || '', /Stable.*暂无正式 Release.*Latest.*与目标不同/s);
  assert.equal(document.querySelector<HTMLAnchorElement>('dialog #update-results a')!.hostname, 'github.com');
  assert.ok(document.querySelector<HTMLAnchorElement>('dialog #update-results a')!.href.endsWith('b'.repeat(40)));
  document.querySelector<HTMLButtonElement>('dialog .dialog-close-btn')!.click();
  assert.equal(document.querySelector('dialog'), null);

  root.querySelector<HTMLButtonElement>('#check-updates')!.click();
  await flush();
  const closedResults = document.querySelector('dialog #update-results')!;
  document.querySelector('dialog')!.dispatchEvent(new dom.window.Event('cancel', { cancelable: true }));
  assert.equal(checkSignal?.aborted, true, 'dismissing dialog must cancel its request');
  assert.equal(document.querySelector('dialog'), null);
  const releaseClosedCheck = releaseCheck!;
  pendingCheck = false;
  root.querySelector<HTMLButtonElement>('#check-updates')!.click();
  await flush();
  const reopenedResults = document.querySelector('dialog #update-results')!;
  assert.match(reopenedResults.textContent || '', /Stable/);
  releaseClosedCheck();
  await flush();
  assert.equal(closedResults.textContent, '正在检查 GitHub…', 'late responses must not update a dismissed dialog');
  assert.equal(document.querySelector('dialog #update-results'), reopenedResults);
  cleanup();
  assert.equal(document.querySelector('dialog'), null, 'page cleanup must close the update dialog');

  pendingCheck = false;
  failCheck = true;
  instanceState.select({ id: 'synthetic-instance', name: 'Synthetic', enabled: false, created_at: '' });
  cleanup = mountVersionControl(root);
  await flush();
  root.querySelector<HTMLButtonElement>('#check-updates')!.click();
  await flush();
  assert.match(document.querySelector('dialog #update-results')!.textContent || '', /HTTP 503/);
  assert.equal(root.querySelector<HTMLButtonElement>('#check-updates')!.disabled, false);
  cleanup();

  failCheck = false;
  pendingCheck = true;
  cleanup = mountVersionControl(root);
  await flush();
  root.querySelector<HTMLButtonElement>('#check-updates')!.click();
  await flush();
  cleanup();
  assert.equal(checkSignal?.aborted, true, 'leaving page must cancel the check');
  assert.equal(document.querySelector('dialog'), null);
  root.textContent = 'replacement page';
  releaseCheck!();
  await flush();
  assert.equal(root.textContent, 'replacement page', 'late responses must not render after cleanup');

  pendingVersion = true;
  cleanup = mountVersionControl(root);
  await flush();
  cleanup();
  const detachedVersion = root.querySelector('#build-version')!;
  releaseVersion!();
  await flush();
  assert.equal(detachedVersion.textContent, '…', 'late local-version response must not render after cleanup');
} finally {
  instanceState.select(null);
  dom.window.close();
  for (const [key, descriptor] of originals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor);
    else Reflect.deleteProperty(globalThis, key);
  }
}
console.log('PASS: empty instance frameworks, control-plane version/update checks, credentials and request lifecycle');
