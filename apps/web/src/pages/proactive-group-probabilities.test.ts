import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  groupProbabilityIdentity, normalizeGroupProbability, parseGroupProbabilities, serializeGroupProbabilities,
} from './proactive-group-probabilities.ts';

test('group probability clamps only negative, invalid and above-one values', () => {
  for (const [value, expected] of [
    ['', 0.01], ['bad', 0.01], ['0.3tail', 0.01], ['NaN', 0.01], ['Infinity', 0.01],
    ['1e999', 0.01], ['0x1', 0.01], [null, 0.01], [true, 0.01], [[0.8], 0.01],
    ['-0.5', 0.01], ['0', 0], ['0.001', 0.001], ['0.1234', 0.1234], [' 0.25 ', 0.25],
    ['+.25', 0.25], ['2e-1', 0.2], ['1', 1], ['1.5', 1], [100, 1],
  ] as const) assert.equal(normalizeGroupProbability(value), expected, String(value));
});

test('probability identities preserve platform boundaries and round-trip safely', () => {
  const values = parseGroupProbabilities('{"synthetic":0.25,"Telegram:synthetic":0.38,"discord:room:synthetic":0.7,"qq:zero":0,"qq:bad":"bad","__proto__":0.8}');
  for (const alias of ['qq:synthetic', 'onebot:synthetic', 'aiocqhttp:synthetic', 'synthetic']) {
    assert.equal(values.get(groupProbabilityIdentity(alias)), 0.25);
  }
  assert.equal(values.get(groupProbabilityIdentity('telegram:synthetic')), 0.38);
  assert.equal(values.get(groupProbabilityIdentity('discord:synthetic')), undefined);
  assert.equal(values.get(groupProbabilityIdentity('discord:room:synthetic')), 0.7);
  assert.equal(values.get('qq:zero'), 0);
  assert.equal(values.get('qq:bad'), 0.01);
  assert.equal(values.get('qq:__proto__'), 0.8);
  assert.deepEqual(parseGroupProbabilities(serializeGroupProbabilities(values)), values);
  assert.equal(serializeGroupProbabilities(new Map()), '');
  for (const raw of ['broken', '[]', 'null', '0', '{"":0.2}', '{"qq:":0.2}', '{"a b":0.2}', '{"synthetic":0.2,"OneBot:synthetic":0.3}']) {
    assert.throws(() => parseGroupProbabilities(raw), raw);
  }
});
