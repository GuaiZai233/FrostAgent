import assert from 'node:assert/strict';
import { create } from '@bufbuild/protobuf';
import { StickerItemSchema } from '@frostagent/proto';
import { StickerFeed } from '../src/pages/sticker-feed';
import { mountStickersPage } from '../src/pages/stickers';
import { instanceState } from '../src/instance-state';
import { toast } from '../src/components/toast';
import type { createInstanceAPI } from '../src/api/client';

const sticker = (id: string) =>
  create(StickerItemSchema, {
    id,
    description: `description-${id}`,
    status: 'ready',
  });
const batch = (ids: string[], nextPageToken = '', totalCount = ids.length) => ({
  stickers: ids.map(sticker),
  nextPageToken,
  totalCount,
});
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}
const settle = async () => {
  for (let i = 0; i < 8; i++) await Promise.resolve();
};

// Only one request may advance a cursor. Retry keeps visible cards and retries
// exactly the failed token, while duplicate IDs and repeated tokens cannot loop.
{
  const pending = deferred<ReturnType<typeof batch>>();
  const calls: string[] = [];
  let fail = true;
  const feed = new StickerFeed(
    async (token) => {
      calls.push(token);
      if (!token) return pending.promise;
      if (fail) throw new Error('synthetic slow resource failure');
      return batch(['first', 'second', 'second'], 'next', 2);
    },
    () => {},
  );
  const first = feed.reset();
  await feed.loadMore();
  assert.deepEqual(calls, ['']);
  assert.equal(feed.loading, true);
  pending.resolve(batch(['first'], 'next', 2));
  await first;
  await feed.loadMore();
  assert.equal(feed.error, 'synthetic slow resource failure');
  assert.deepEqual(
    feed.stickers.map((item) => item.id),
    ['first'],
  );
  assert.equal(feed.hasMore, true);
  fail = false;
  await feed.loadMore();
  assert.deepEqual(calls, ['', 'next', 'next']);
  assert.deepEqual(
    feed.stickers.map((item) => item.id),
    ['first', 'second'],
  );
  assert.equal(feed.error, '');
  assert.equal(feed.hasMore, false);
  await feed.loadMore();
  assert.equal(calls.length, 3);
}

for (const rejectOld of [false, true]) {
  const old = deferred<ReturnType<typeof batch>>();
  let calls = 0;
  const feed = new StickerFeed(
    async () => (++calls === 1 ? old.promise : batch(['new'])),
    () => {},
  );
  const first = feed.reset();
  await feed.reset();
  if (rejectOld) old.reject(new Error('obsolete failure'));
  else old.resolve(batch(['obsolete'], 'obsolete-cursor', 10));
  await first;
  assert.deepEqual(
    feed.stickers.map((item) => item.id),
    ['new'],
  );
  assert.equal(feed.hasMore, false);
  assert.equal(feed.loading, false);
  assert.equal(feed.error, '');
}

{
  const pending = deferred<ReturnType<typeof batch>>();
  let notifications = 0;
  let calls = 0;
  const feed = new StickerFeed(
    () => {
      calls++;
      return pending.promise;
    },
    () => {
      notifications++;
    },
  );
  const request = feed.reset();
  const before = notifications;
  feed.dispose();
  pending.resolve(batch(['late']));
  await request;
  await feed.reset();
  await feed.loadMore();
  assert.equal(notifications, before);
  assert.equal(calls, 1);
  assert.deepEqual(feed.stickers, []);
}

type MockEvent = { target: MockElement; key?: string };
class MockElement {
  private html = '';
  private children = new Map<string, MockElement>();
  private listeners = new Map<string, (event: MockEvent) => unknown>();
  replacements = 0;
  additions = 0;
  value = '';
  textContent = '';
  disabled = false;
  closed = false;
  className = '';
  style: Record<string, string> = {};
  dataset: Record<string, string> = {};
  attributes: Record<string, string> = {};
  files: File[] = [];
  get innerHTML() {
    return this.html;
  }
  set innerHTML(value: string) {
    this.html = value;
    this.replacements++;
    this.children.clear();
  }
  insertAdjacentHTML(_position: string, value: string) {
    this.html += value;
    this.additions++;
  }
  addEventListener(
    name: string,
    handler: (event: MockEvent) => unknown,
    options?: AddEventListenerOptions,
  ) {
    this.listeners.set(name, handler);
    options?.signal?.addEventListener('abort', () =>
      this.listeners.delete(name),
    );
  }
  trigger(name: string, target = this, key?: string) {
    return this.listeners.get(name)?.({ target, key });
  }
  querySelector(selector: string): MockElement {
    if (!this.children.has(selector))
      this.children.set(selector, new MockElement());
    return this.children.get(selector)!;
  }
  querySelectorAll() {
    return [];
  }
  closest() {
    return this.dataset.action ? this : null;
  }
  setAttribute(name: string, value: string) {
    this.attributes[name] = value;
  }
  showModal() {}
  close() {
    this.closed = true;
  }
  remove() {}
  focus() {}
}

class MockObserver {
  static instances: MockObserver[] = [];
  disconnected = false;
  observeCount = 0;
  constructor(
    private callback: (entries: { isIntersecting: boolean }[]) => void,
  ) {
    MockObserver.instances.push(this);
  }
  observe() {
    this.observeCount++;
  }
  unobserve() {}
  disconnect() {
    this.disconnected = true;
  }
  intersect() {
    this.callback([{ isIntersecting: true }]);
  }
}

const dialogs: MockElement[] = [];
Object.assign(globalThis, {
  IntersectionObserver: MockObserver,
  document: {
    querySelectorAll: () => [],
    createElement: () => new MockElement(),
    body: { appendChild: (element: MockElement) => dialogs.push(element) },
  },
  window: Object.assign(new EventTarget(), {
    location: { origin: 'http://localhost' },
  }),
});
instanceState.select({
  id: 'synthetic-sticker-instance',
  name: 'Test',
  enabled: true,
  created_at: '',
});
toast.error = (message) => {
  throw new Error(message);
};
toast.success = () => {};
toast.warning = () => {};
const requestArgs: unknown[][] = [];
let list: (...args: unknown[]) => Promise<ReturnType<typeof batch>>;
let statsCalls = 0;
let uploads = 0;
let deletes = 0;
let updates = 0;
const api = {
  listStickers: (...args: unknown[]) => {
    requestArgs.push(args);
    return list(...args);
  },
  getStickerStats: async () => {
    statsCalls++;
    return { total: 2, ready: 2, unsummarized: 0 };
  },
  uploadSticker: async () => {
    uploads++;
    return { success: true };
  },
  deleteSticker: async () => {
    deletes++;
    return { success: true };
  },
  updateStickerKeywords: async () => {
    updates++;
    return { success: true };
  },
} as unknown as ReturnType<typeof createInstanceAPI>;
function mount() {
  requestArgs.length = 0;
  const root = new MockElement();
  const dispose = mountStickersPage(root as unknown as HTMLElement, api);
  return {
    root,
    dispose,
    grid: root.querySelector('#sticker-grid'),
    footer: root.querySelector('#sticker-load-more'),
    observer: MockObserver.instances.at(-1)!,
  };
}
function action(name: string, id?: string) {
  const button = new MockElement();
  button.dataset.action = name;
  if (id) button.dataset.id = id;
  return button;
}

// Exercise the real page: observer requests, slow loading, append-only DOM,
// inline failure/retry, keyboard-accessible fallback, and terminal state.
{
  let next = deferred<ReturnType<typeof batch>>();
  list = async (_size, token) =>
    token ? next.promise : batch(['first'], 'cursor-2', 2);
  const page = mount();
  await settle();
  assert.doesNotMatch(page.root.innerHTML, /pagination|page-size/);
  assert.deepEqual(requestArgs, [[24, '', '', '', 'created_at', false]]);
  const replacements = page.grid.replacements;
  page.observer.intersect();
  page.observer.intersect();
  assert.equal(
    requestArgs.length,
    2,
    'repeated intersections sent duplicate requests',
  );
  assert.match(page.footer.innerHTML, /正在加载表情包，请稍候/);
  assert.match(page.grid.innerHTML, /description-first/);
  assert.equal(
    page.grid.replacements,
    replacements,
    'slow load discarded existing image nodes',
  );
  next.reject(new Error('<synthetic resource error>'));
  await settle();
  assert.match(page.footer.innerHTML, /&lt;synthetic resource error&gt;/);
  assert.match(page.footer.innerHTML, /重试加载/);
  page.observer.intersect();
  assert.equal(
    requestArgs.length,
    2,
    'failed loads must wait for an explicit retry',
  );
  next = deferred<ReturnType<typeof batch>>();
  page.footer.trigger('click', action('load-stickers'));
  assert.equal(requestArgs.length, 3);
  assert.equal(requestArgs.at(-1)?.[1], 'cursor-2');
  next.resolve(batch(['second'], '', 2));
  await settle();
  assert.match(page.grid.innerHTML, /description-first/);
  assert.match(page.grid.innerHTML, /description-second/);
  assert.equal(
    page.grid.replacements,
    replacements,
    'appending a batch replaced existing image nodes',
  );
  assert.equal(page.grid.additions, 2);
  assert.match(page.footer.innerHTML, /已加载全部 2 张/);
  page.observer.intersect();
  assert.equal(requestArgs.length, 3, 'terminal cursor must stop loading');
  page.dispose();
  assert.equal(page.observer.disconnected, true);
}

// Every filter/sort changes the query and resets to the first cursor. A late
// previous page must not leak into the new result, including after unmount.
{
  const pending = deferred<ReturnType<typeof batch>>();
  list = async (_size, token, _status, search) =>
    token
      ? pending.promise
      : batch([String(search || 'initial')], 'cursor-2', 2);
  const page = mount();
  await settle();
  page.observer.intersect();
  page.root.querySelector('#sticker-search-input').value = 'filtered';
  page.root.querySelector('#sticker-search-btn').trigger('click');
  await settle();
  pending.resolve(batch(['obsolete']));
  await settle();
  assert.match(page.grid.innerHTML, /description-filtered/);
  assert.doesNotMatch(
    page.grid.innerHTML,
    /description-initial|description-obsolete/,
  );
  assert.deepEqual(requestArgs.at(-1), [
    24,
    '',
    '',
    'filtered',
    'created_at',
    false,
  ]);
  page.root.querySelector('#sticker-status-select').value = 'ready';
  page.root.querySelector('#sticker-status-select').trigger('change');
  await settle();
  assert.deepEqual(requestArgs.at(-1), [
    24,
    '',
    'ready',
    'filtered',
    'created_at',
    false,
  ]);
  page.root.querySelector('#sticker-sort-select').value = 'weight';
  page.root.querySelector('#sticker-sort-select').trigger('change');
  await settle();
  page.root.querySelector('#sticker-sort-order-select').value = 'desc';
  page.root.querySelector('#sticker-sort-order-select').trigger('change');
  await settle();
  assert.deepEqual(requestArgs.at(-1), [
    24,
    '',
    'ready',
    'filtered',
    'weight',
    true,
  ]);
  page.root.querySelector('#sticker-refresh-btn').trigger('click');
  await settle();
  assert.deepEqual(requestArgs.at(-1), [24, '', '', '', 'created_at', false]);
  const late = deferred<ReturnType<typeof batch>>();
  list = () => late.promise;
  page.observer.intersect();
  const before = page.grid.innerHTML;
  const requestCount = requestArgs.length;
  page.dispose();
  late.resolve(batch(['after-unmount']));
  await settle();
  page.observer.intersect();
  assert.equal(page.grid.innerHTML, before);
  assert.equal(requestArgs.length, requestCount);
}

// Mutations restart from the first cursor: retaining an offset after deletion
// or upload would skip or repeat records. Editing remains wired through delegation.
{
  list = async (_size, token) =>
    batch([token ? 'second' : 'first'], token ? '' : 'cursor-2', 2);
  const page = mount();
  await settle();
  page.observer.intersect();
  await settle();
  const edit = page.grid.trigger('click', action('edit-sticker', 'second'));
  await edit;
  const dialog = dialogs.at(-1)!;
  dialog.querySelector('#edit-sticker-desc').value = 'updated';
  dialog.querySelector('#edit-sticker-keywords').value = 'synthetic, updated';
  await dialog.querySelector('#edit-sticker-save').trigger('click');
  await settle();
  assert.equal(updates, 1);
  assert.equal(dialog.closed, true);
  assert.equal(requestArgs.at(-1)?.[1], '');
  const statsBefore = statsCalls;
  const deleting = page.grid.trigger(
    'click',
    action('delete-sticker', 'first'),
  );
  dialogs.at(-1)!.querySelector('#confirm-ok-btn').trigger('click');
  await deleting;
  await settle();
  assert.equal(deletes, 1);
  assert.equal(requestArgs.at(-1)?.[1], '');
  assert.ok(statsCalls > statsBefore);
  const upload = page.root.querySelector('#sticker-upload-input');
  upload.files = [
    new File(['synthetic'], 'synthetic.png', { type: 'image/png' }),
  ];
  await upload.trigger('change');
  await settle();
  assert.equal(uploads, 1);
  assert.equal(requestArgs.at(-1)?.[1], '');
  assert.equal(upload.value, '');
  page.dispose();
}

{
  list = async () => batch([]);
  const page = mount();
  await settle();
  assert.match(page.grid.innerHTML, /暂无表情包记录/);
  assert.match(page.footer.innerHTML, /已加载全部 0 张/);
  page.observer.intersect();
  assert.equal(requestArgs.length, 1);
  page.dispose();
}

// An initial failure remains retryable, including browsers without an observer.
{
  Object.assign(globalThis, { IntersectionObserver: undefined });
  let fail = true;
  list = async () => {
    if (fail) throw new Error('synthetic first-page failure');
    return batch(['recovered']);
  };
  const page = mount();
  await settle();
  assert.match(page.grid.innerHTML, /未能加载表情包/);
  assert.match(page.footer.innerHTML, /synthetic first-page failure/);
  fail = false;
  page.footer.trigger('click', action('load-stickers'));
  await settle();
  assert.deepEqual(requestArgs.map((args) => args[1]), ['', '']);
  assert.match(page.grid.innerHTML, /description-recovered/);
  assert.match(page.footer.innerHTML, /已加载全部 1 张/);
  page.dispose();
  Object.assign(globalThis, { IntersectionObserver: MockObserver });
}

console.log(
  'PASS: sticker infinite scroll, append-only rendering, retry, filters, mutations, and lifecycle',
);
