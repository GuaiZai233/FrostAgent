import test from 'node:test';
import assert from 'node:assert/strict';
import {
  groupIdFromSessionId,
  parseWhitelistGroups,
  formatGroupOption,
} from './proactive-whitelist.ts';

test('groupIdFromSessionId extracts correct group ID from various session ID patterns', () => {
  assert.equal(groupIdFromSessionId('group:12345'), '12345');
  assert.equal(groupIdFromSessionId('GROUP:12345'), '12345');
  assert.equal(groupIdFromSessionId('onebot:group:67890'), '67890');
  assert.equal(groupIdFromSessionId('astrbot:group:112233'), '112233');
  assert.equal(groupIdFromSessionId('aiocqhttp:group:998877'), '998877');
  assert.equal(groupIdFromSessionId('telegram:group:555'), '555');

  // Non-group sessions or invalid formats
  assert.equal(groupIdFromSessionId('private:12345'), '');
  assert.equal(groupIdFromSessionId('astrbot:private:12345'), '');
  assert.equal(groupIdFromSessionId('unknown_session'), '');
  assert.equal(groupIdFromSessionId(''), '');
});

test('parseWhitelistGroups correctly parses, trims, filters empty and deduplicates groups', () => {
  assert.deepEqual(parseWhitelistGroups('123, 456, 789'), ['123', '456', '789']);
  assert.deepEqual(parseWhitelistGroups('123; 456 \n 789\t 101'), ['123', '456', '789', '101']);
  assert.deepEqual(parseWhitelistGroups('123, 123, 456, 456'), ['123', '456']);
  assert.deepEqual(parseWhitelistGroups('  '), []);
  assert.deepEqual(parseWhitelistGroups(',;; \n '), []);
  assert.deepEqual(parseWhitelistGroups(''), []);
});

test('formatGroupOption formats label with group name when cached, or plain group id otherwise', () => {
  assert.equal(formatGroupOption('123456', '王源粉丝群'), '123456（王源粉丝群）');
  assert.equal(formatGroupOption('34567', ''), '34567');
  assert.equal(formatGroupOption('34567', '   '), '34567');
  assert.equal(formatGroupOption('34567', undefined), '34567');
});
