import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { setImmediate } from 'node:timers/promises';
import { mountOverviewPage } from '../src/pages/overview';
import { mountSettingsPage } from '../src/pages/settings';
import { instanceState } from '../src/instance-state';

const { JSDOM } = createRequire(resolve('package.json'))('jsdom');
const dom = new JSDOM('<body><main id="root"></main></body>', { url: 'http://panel.example/' });
const originals = { window: globalThis.window, document: globalThis.document, Event: globalThis.Event, fetch: globalThis.fetch };
Object.assign(globalThis, { window: dom.window, document: dom.window.document, Event: dom.window.Event });
dom.window.HTMLDialogElement.prototype.showModal = function () { this.open = true; };
dom.window.HTMLDialogElement.prototype.close = function () { this.open = false; this.dispatchEvent(new dom.window.Event('close')); };
const root = document.querySelector<HTMLElement>('#root')!;
let tick = () => {};
dom.window.setInterval = (callback: () => void) => { tick = callback; return 1; };
const instance = { id: 'fixture-a', name: 'Example', enabled: true, created_at: '' };
const source = { ...instance, id: 'fixture-b', name: 'Source' };
let pending: (() => void) | undefined;
let deferOverview = false;
let overviewLoads = 0;
globalThis.fetch = async (input) => {
  const path = new URL(input instanceof Request ? input.url : String(input), dom.window.location.origin).pathname;
  if (path === '/api/instances') return Response.json({ instances: [instance, source], next_name: 'Example' });
  assert.ok(path.endsWith('/GetOverview'), path);
  overviewLoads++;
  if (deferOverview) await new Promise<void>((resolve) => { pending = resolve; });
  return Response.json({ status: 2, botName: 'Example', uptimeSeconds: '1', totalMessagesProcessed: '3', tools: Array.from({ length: 16 }, (_, i) => ({ name: `tool-${i}`, description: '<img src=x onerror=alert(1)> Example' })) });
};
const flush = async () => { for (let i = 0; i < 8; i++) await setImmediate(); };

try {
  instanceState.select(instance);
  let cleanup = mountOverviewPage(root);
  await flush();
  assert.equal(root.querySelectorAll('#overview-tools-strip article').length, 10);
  assert.equal(root.querySelectorAll('#overview-tools-strip img').length, 0, 'tool descriptions must remain escaped');
  let rail = root.querySelector<HTMLElement>('#overview-tools-strip')!;
  rail.scrollLeft = 512;
  root.querySelector<HTMLButtonElement>('#overview-all-tools')!.focus();
  tick();
  await flush();
  rail = root.querySelector<HTMLElement>('#overview-tools-strip')!;
  assert.equal(rail.scrollLeft, 512, 'polling must preserve horizontal scroll');
  assert.equal(document.activeElement?.id, 'overview-all-tools', 'polling must preserve tool-list keyboard focus');
  root.querySelector<HTMLButtonElement>('#overview-all-tools')!.click();
  assert.equal(document.querySelectorAll('dialog .overview-all-tools article').length, 16);
  cleanup();
  assert.equal(document.querySelectorAll('dialog').length, 0, 'leaving overview must close its tool dialog');

  deferOverview = true;
  const loadsBefore = overviewLoads;
  cleanup = mountOverviewPage(root);
  tick();
  tick();
  await flush();
  assert.equal(overviewLoads, loadsBefore + 1, 'slow polling must not overlap requests');
  cleanup();
  pending?.();
  await flush();
  assert.equal(root.querySelectorAll('#overview-tools-strip').length, 0, 'late responses must not render after cleanup');

  instanceState.select(null);
  cleanup = mountSettingsPage(root);
  assert.equal(root.querySelector<HTMLButtonElement>('#settings-quick-config')!.disabled, true);
  cleanup();
  instanceState.select(instance);
  cleanup = mountSettingsPage(root);
  const quick = root.querySelector<HTMLButtonElement>('#settings-quick-config')!;
  assert.equal(quick.disabled, false);
  assert.ok(quick.compareDocumentPosition(root.querySelector('[href="#/settings/backup"]')!) & dom.window.Node.DOCUMENT_POSITION_FOLLOWING);
  quick.click();
  await flush();
  const sourceSelect = document.querySelector<HTMLSelectElement>('#instance-copy-source')!;
  assert.ok(sourceSelect, 'settings entry must open the existing quick-configuration dialog');
  assert.deepEqual(Array.from(sourceSelect.options, (option) => option.value), ['', source.id]);
  document.querySelector<HTMLButtonElement>('dialog .dialog-close-btn')!.click();
  cleanup();
} finally {
  instanceState.select(null);
  dom.window.close();
  Object.assign(globalThis, originals);
}
console.log('PASS: overview dialog, polling scroll/focus, request lifecycle and settings quick configuration in a DOM');
