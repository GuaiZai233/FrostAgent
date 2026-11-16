import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { dedicatedSettingKeys, settingFields } from './backend-setting-fields.ts';

type LifecycleEntry = {
  key: string;
  scope: string;
  catalog: string;
  instanceRestart: boolean;
  controlRestart: boolean;
  sqlHot: boolean;
  frontend: string;
  seed: string | null;
};

test('frontend lifecycle metadata matches the reviewed backend inventory', () => {
  const rows: LifecycleEntry[] = JSON.parse(readFileSync(
    new URL('../../../../internal/settingscontract/testdata/lifecycle.json', import.meta.url), 'utf8',
  ));
  assert.ok(Array.isArray(rows) && rows.length > 0, 'lifecycle inventory must contain entries');
  const entries = new Map<string, string>();
  for (const row of rows) {
    const { key, frontend: expected } = row;
    assert.deepEqual(Object.keys(row).sort(), [
      'catalog', 'controlRestart', 'frontend', 'instanceRestart', 'key', 'scope', 'seed', 'sqlHot',
    ], `${key}: lifecycle inventory schema drift`);
    assert.ok(typeof key === 'string' && key.length > 0 && !entries.has(key), `${key}: empty or duplicate inventory key`);
    assert.ok(['instance', 'global', 'shared'].includes(row.scope), `${key}: invalid scope`);
    assert.ok(['absent', 'live', 'restart'].includes(row.catalog), `${key}: invalid catalog state`);
    assert.ok(['hot', 'reload', 'dedicated', 'hidden'].includes(expected), `${key}: invalid frontend state`);
    for (const field of ['instanceRestart', 'controlRestart', 'sqlHot'] as const) {
      assert.equal(typeof row[field], 'boolean', `${key}: invalid ${field}`);
    }
    assert.ok(row.seed === null || typeof row.seed === 'string', `${key}: invalid seed`);
    entries.set(key, expected);
    const field = settingFields[key];
    const actual = dedicatedSettingKeys.has(key) ? 'dedicated' : field ? (field.hot ? 'hot' : 'reload') : 'hidden';
    assert.equal(actual, expected, `${key}: frontend lifecycle=${actual}, inventory=${expected}`);
  }
  for (const key of [...Object.keys(settingFields), ...dedicatedSettingKeys]) {
    assert.ok(entries.has(key), `${key}: frontend field has no lifecycle inventory entry`);
  }
});
