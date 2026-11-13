import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { dedicatedSettingKeys, settingFields } from './backend-setting-fields.ts';

test('frontend lifecycle metadata matches the reviewed backend inventory', () => {
  const text = readFileSync(new URL('../../../../internal/settingscontract/testdata/lifecycle.tsv', import.meta.url), 'utf8');
  const [header, ...lines] = text.trimEnd().split(/\r?\n/);
  assert.equal(header, 'key\tscope\tcatalog\tinstance_restart\tcontrol_restart\tsql_hot\tfrontend\tseed');
  const entries = new Map<string, string>();
  for (const line of lines) {
    const [key, , , , , , expected] = line.split('\t');
    assert.ok(!entries.has(key), `${key}: duplicate lifecycle inventory entry`);
    entries.set(key, expected);
    const field = settingFields[key];
    const actual = dedicatedSettingKeys.has(key) ? 'dedicated' : field ? (field.hot ? 'hot' : 'reload') : 'hidden';
    assert.equal(actual, expected, `${key}: frontend lifecycle=${actual}, inventory=${expected}`);
  }
  for (const key of [...Object.keys(settingFields), ...dedicatedSettingKeys]) {
    assert.ok(entries.has(key), `${key}: frontend field has no lifecycle inventory entry`);
  }
});
