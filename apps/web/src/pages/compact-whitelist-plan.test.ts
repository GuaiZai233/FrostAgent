import test from 'node:test';
import assert from 'node:assert/strict';
import { planCompactGroupChange } from './compact-whitelist-plan.ts';

test('adding the first group with an unset switch pins the effective disabled state', () => {
  assert.deepEqual(
    planCompactGroupChange({ enabledRaw: undefined, effectiveEnabled: false }, ['123']),
    [
      { key: 'ENABLE_GROUP_COMPACT_WHITELIST', value: 'false' },
      { key: 'GROUP_COMPACT_WHITELIST', value: '123' },
    ],
  );
});

test('an unset switch with an existing list keeps its implied enabled state when the list changes', () => {
  assert.deepEqual(
    planCompactGroupChange({ enabledRaw: '', effectiveEnabled: true }, ['123', '456']),
    [
      { key: 'ENABLE_GROUP_COMPACT_WHITELIST', value: 'true' },
      { key: 'GROUP_COMPACT_WHITELIST', value: '123,456' },
    ],
  );
});

test('an explicit switch is left alone and only the group list is written', () => {
  assert.deepEqual(
    planCompactGroupChange({ enabledRaw: 'true', effectiveEnabled: true }, []),
    [{ key: 'GROUP_COMPACT_WHITELIST', value: '' }],
  );
});
