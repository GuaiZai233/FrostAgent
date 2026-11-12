import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { mountAwarenessSettings, validAwarenessInterval } from '../src/pages/awareness-settings';

const { JSDOM } = createRequire(resolve('package.json'))('jsdom');
const dom = new JSDOM('<body><main></main></body>');
const saved = Object.getOwnPropertyDescriptor(globalThis, 'document');
Object.assign(globalThis, { document: dom.window.document });
try {
  const root = document.querySelector('main')!;
  const values = new Map<string, string>();
  const flush = mountAwarenessSettings(root, {
    get: (key) => values.get(key), change: (key, value) => { values.set(key, value); }, canEdit: () => true,
    groups: new Map([['fixture-group', 'Persistent den']]), privateIDs: ['fixture-user'], dirty: () => {},
  });
  assert.match(root.textContent!, /不定期向Bot自动注入提示词/);
  assert.match(root.textContent!, /Persistent den/);
  for (const kind of ['private', 'group']) {
    const input = root.querySelector<HTMLInputElement>(`[data-awareness-input="${kind}"]`)!;
    input.value = `fixture-${kind}`;
    root.querySelector<HTMLButtonElement>(`[data-awareness-add="${kind}"]`)!.click();
    input.value = `second-${kind}`;
    root.querySelector<HTMLButtonElement>(`[data-awareness-add="${kind}"]`)!.click();
    assert.equal(values.get(`PROACTIVE_AWARENESS_${kind.toUpperCase()}_WHITELIST`), `fixture-${kind},second-${kind}`);
    assert.equal(values.get(`ENABLE_PROACTIVE_AWARENESS_${kind.toUpperCase()}_WHITELIST`), 'false', 'adding must preserve the displayed switch');
  }
  const privateInput = root.querySelector<HTMLInputElement>('[data-awareness-input="private"]')!;
  privateInput.value = 'pending-user';
  assert.equal(flush(), true);
  assert.match(values.get('PROACTIVE_AWARENESS_PRIVATE_WHITELIST')!, /pending-user/);
  assert.doesNotMatch(values.get('PROACTIVE_AWARENESS_GROUP_WHITELIST')!, /pending-user/);
  root.querySelector<HTMLButtonElement>('[data-awareness-tags="group"] button')!.click();
  assert.equal(values.get('PROACTIVE_AWARENESS_GROUP_WHITELIST'), 'second-group');
  for (const value of ['', '30m - 2h', '1h - 1h', '0.5h - 1h']) assert.equal(validAwarenessInterval(value), true, value);
  for (const value of ['30s - 1h', '0m - 1h', '2h - 30m', '1h', '1m - 999999999h']) assert.equal(validAwarenessInterval(value), false, value);
  const interval = root.querySelector<HTMLInputElement>('#awareness-interval')!;
  interval.value = '2h - 30m';
  interval.dispatchEvent(new dom.window.Event('input'));
  assert.equal(flush(), false, 'invalid range must block save');
  assert.equal(interval.validity.valid, false);
  console.log('awareness settings: independent lists, draft edits, pending values and validation OK');
} finally {
  dom.window.close();
  if (saved) Object.defineProperty(globalThis, 'document', saved);
  else delete (globalThis as Record<string, unknown>).document;
}
