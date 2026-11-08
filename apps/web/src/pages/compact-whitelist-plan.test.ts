import test from 'node:test';
import assert from 'node:assert/strict';
import {
  COMPACT_WHITELIST_ENABLED_KEY,
  COMPACT_WHITELIST_GROUPS_KEY,
  planCompactGroupChange,
} from './compact-whitelist-plan.ts';

test('group compact whitelist keys match the backend environment names', () => {
  assert.equal(COMPACT_WHITELIST_ENABLED_KEY, 'ENABLE_GROUP_COMPACT_WHITELIST');
  assert.equal(COMPACT_WHITELIST_GROUPS_KEY, 'GROUP_COMPACT_WHITELIST');
});

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
