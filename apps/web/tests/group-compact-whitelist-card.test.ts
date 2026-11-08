import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { mountGroupCompactWhitelist } from '../src/pages/group-compact-whitelist';
import { isWhitelistEnabled } from '../src/pages/proactive-whitelist';

const { JSDOM } = createRequire(resolve('package.json'))('jsdom');
const dom = new JSDOM('<body><div id="slot"></div></body>');
Object.assign(globalThis, { window: dom.window, document: dom.window.document, Event: dom.window.Event });
const slot = document.querySelector<HTMLElement>('#slot')!;

// A draft backed by a plain map, mirroring how backend-settings reads and writes values.
const draft = new Map<string, string>();
const deps = {
  get: (key: string) => draft.get(key),
  change: (key: string, value: string) => { draft.set(key, value); },
  canEdit: () => true,
  sessionGroups: () => new Map<string, string>([['123', '测试群']]),
  effectiveEnabled: isWhitelistEnabled,
};
const tagIds = () => [...slot.querySelectorAll<HTMLElement>('[data-compact-group-id]')]
  .map((el) => el.dataset.compactGroupId);
const typeAndAdd = (value: string) => {
  const input = slot.querySelector<HTMLInputElement>('#compact-whitelist-input')!;
  input.value = value;
  slot.querySelector<HTMLButtonElement>('#compact-whitelist-add-btn')!.click();
};

// Consecutive adds on an empty list must accumulate, not overwrite each other.
mountGroupCompactWhitelist(slot, deps);
typeAndAdd('123');
typeAndAdd('456');
assert.equal(draft.get('GROUP_COMPACT_WHITELIST'), '123,456');
assert.deepEqual(tagIds(), ['123', '456']);
assert.equal(draft.get('ENABLE_GROUP_COMPACT_WHITELIST'), 'false', 'unset switch pins its effective state');

// Consecutive removes must each see the previous removal.
typeAndAdd('789');
const removeButton = (id: string) => slot.querySelector<HTMLButtonElement>(`[data-compact-group-id="${id}"]`)!;
removeButton('123').click();
assert.equal(draft.get('GROUP_COMPACT_WHITELIST'), '456,789');
removeButton('456').click();
assert.equal(draft.get('GROUP_COMPACT_WHITELIST'), '789');
assert.deepEqual(tagIds(), ['789']);

dom.window.close();
console.log('group compact whitelist card: consecutive add/remove OK');
