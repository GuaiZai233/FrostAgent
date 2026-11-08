import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { create } from '@bufbuild/protobuf';
import { TokenUsageOverviewSchema, TokenUsageModuleSchema } from '@frostagent/proto';
import { renderTokenUsage } from '../src/components/token-usage';

const { JSDOM } = createRequire(resolve('package.json'))('jsdom');
const dom = new JSDOM('<body></body>');
const render = (modules: Parameters<typeof create<typeof TokenUsageModuleSchema>>[1][] = [], incomplete = false) => {
  dom.window.document.body.innerHTML = renderTokenUsage(create(TokenUsageOverviewSchema, {
    available: true, incomplete, modules: modules.map((row) => create(TokenUsageModuleSchema, row)),
  }));
  return dom.window.document;
};
try {
  let doc = render([{ module: 'dialogue', requests: 1n, input: { value: 10n }, output: { value: 5n }, total: { value: 15n }, cached: { value: 8n }, cacheHits: { value: 1n } }]);
  const cards = [...doc.querySelectorAll('.token-usage-grid article')];
  assert.equal(cards[0].querySelector('.token-usage-value')!.textContent, '15', 'cache must not be added to total');
  assert.equal(cards[4].querySelector('.token-usage-value')!.textContent, '8');
  assert.equal(cards[5].querySelector('.token-usage-value')!.textContent, '1');
  doc = render([{ module: 'sticker_summary', requests: 2n, input: { value: 10n, unestimatedRequests: 1n }, output: { unestimatedRequests: 2n }, total: { value: 15n, unestimatedRequests: 1n }, cached: { unestimatedRequests: 2n }, cacheHits: { unestimatedRequests: 2n } }], true);
  const cells = [...doc.querySelectorAll('tbody td')].map((cell) => cell.textContent);
  assert.deepEqual(cells, ['2', '10 + unestimated (1 次)', 'unestimated (2 次)', '15 + unestimated (1 次)', 'unestimated (2 次)', 'unestimated (2 次)']);
  assert.equal(doc.querySelector('tbody th')!.textContent, '表情包摘要');
  assert.equal(doc.querySelectorAll('.token-usage-grid article')[3].querySelector('.token-usage-value')!.textContent, '15 + unestimated (1 次)');
  assert.match(doc.querySelector('[role="status"]')!.textContent!, /不完整/);
  doc = render([{ module: 'vision', requests: 1n, input: {}, output: {}, total: {}, cached: {}, cacheHits: {} }]);
  assert.equal(doc.querySelector('tbody td:nth-child(4)')!.textContent, '0', 'reported zero stays known');
  doc = render([{ module: 'vision', requests: 1n }]);
  assert.equal(doc.querySelector('tbody td:nth-child(4)')!.textContent, 'unestimated', 'omitted counter stays unknown');
  doc = render([{ module: '"><img src=x onerror=alert(1)>' }]);
  assert.equal(doc.querySelectorAll('img').length, 0, 'API labels must be escaped');
  assert.match(renderTokenUsage(undefined), /unestimated/);
  assert.match(renderTokenUsage(create(TokenUsageOverviewSchema)), /unestimated/);
} finally { dom.window.close(); }
console.log('PASS: token usage API presence, cache totals, partial values, sticker summary and escaping');
