import test, { mock } from 'node:test';
import assert from 'node:assert/strict';
import { lazyMount } from './lazy-page.ts';
import type { PageMountFn } from './router.ts';

const nextTurn = () => new Promise<void>((resolve) => setImmediate(resolve));

test('lazy page mounts after loading and runs its cleanup', async () => {
  const container = { innerHTML: '' } as HTMLElement;
  let cleaned = 0;
  const cleanup = lazyMount(async () => (page) => {
    page.innerHTML = 'loaded';
    return () => {
      cleaned++;
    };
  })(container);

  assert.match(container.innerHTML, /页面加载中/);
  await nextTurn();
  assert.equal(container.innerHTML, 'loaded');
  if (typeof cleanup === 'function') cleanup();
  assert.equal(cleaned, 1);
});

test('lazy page does not mount after navigation away', async () => {
  let resolveLoad!: (mount: PageMountFn) => void;
  const loading = new Promise<PageMountFn>((resolve) => {
    resolveLoad = resolve;
  });
  const container = { innerHTML: '' } as HTMLElement;
  let mounts = 0;
  const cleanup = lazyMount(() => loading)(container);

  if (typeof cleanup === 'function') cleanup();
  resolveLoad(() => {
    mounts++;
  });
  await nextTurn();
  assert.equal(mounts, 0);
});

test('lazy page reports a failed import', async () => {
  const errorLog = mock.method(console, 'error', () => {});
  try {
    const container = { innerHTML: '' } as HTMLElement;
    lazyMount(async () => {
      throw new Error('chunk unavailable');
    })(container);
    await nextTurn();
    assert.match(container.innerHTML, /页面加载失败/);
    assert.equal(errorLog.mock.callCount(), 1);
  } finally {
    errorLog.mock.restore();
  }
});
