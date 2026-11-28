import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { create } from '@bufbuild/protobuf';
import { StorageUsageItemSchema, StorageUsageOverviewSchema } from '@frostagent/proto';
import { formatBytes, renderStorageUsage } from '../src/components/storage-usage';

const { JSDOM } = createRequire(resolve('package.json'))('jsdom');
const dom = new JSDOM('<body></body>');
const render = (usage: ReturnType<typeof create<typeof StorageUsageOverviewSchema>> | undefined) => {
  dom.window.document.body.innerHTML = renderStorageUsage(usage);
  return dom.window.document;
};
const overview = (rows: Record<string, bigint>) => {
  const items = Object.entries(rows).map(([category, bytes]) => create(StorageUsageItemSchema, { category, bytes }));
  return create(StorageUsageOverviewSchema, {
    available: true, items, totalBytes: Object.values(rows).reduce((a, b) => a + b, 0n),
  });
};

try {
  assert.equal(formatBytes(0), '0 B');
  assert.equal(formatBytes(1023n), '1023 B');
  assert.equal(formatBytes(1536n), '1.5 KB');
  assert.equal(formatBytes(5n * 1024n * 1024n), '5.0 MB');

  assert.equal(renderStorageUsage(undefined), '', 'older backends omit the section');

  let doc = render(overview({ chat: 3000n, images: 1000n, stickers: 0n, memory: 0n, other: 0n }));
  const segments = [...doc.querySelectorAll('.storage-usage-segment')];
  assert.deepEqual(segments.map((s) => (s as HTMLElement).dataset.storageCategory), ['chat', 'images'], 'zero-sized categories draw no segment');
  assert.match((segments[0] as HTMLElement).getAttribute('style')!, /flex-grow:3000/);
  const legend = [...doc.querySelectorAll('.storage-usage-legend li')].map((li) => li.textContent!.replace(/\s+/g, ' ').trim());
  assert.equal(legend.length, 5, 'legend lists every category');
  assert.match(legend[0], /聊天记录 2\.9 KB · 75\.0%/);
  assert.match(legend[1], /图片 1000 B · 25\.0%/);
  assert.match(legend[2], /表情包 0 B · 0\.0%/);
  assert.match(doc.querySelector('[role="img"]')!.getAttribute('aria-label')!, /聊天记录 2\.9 KB/);
  assert.match(doc.querySelector('[data-storage-total]')!.textContent!, /合计 3\.9 KB/);

  doc = render(overview({ chat: 0n, images: 0n }));
  assert.equal(doc.querySelectorAll('.storage-usage-segment').length, 0);
  assert.match(doc.querySelector('.storage-usage-legend')!.textContent!, /0\.0%/);

  doc = render(create(StorageUsageOverviewSchema, { available: false }));
  assert.match(doc.body.textContent!, /暂时无法读取存储占用统计/);
} finally {
  dom.window.close();
}
