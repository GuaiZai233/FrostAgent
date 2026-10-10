import test from 'node:test';
import assert from 'node:assert/strict';
import {
  groupIdFromSessionId,
  parseWhitelistGroups,
  formatGroupOption,
  isWhitelistEnabled,
  ProactiveWhitelistSync,
  type WhitelistAPI,
} from './proactive-whitelist.ts';

test('isWhitelistEnabled normalizes boolean strings case-insensitively with whitespace', () => {
  // Truthy values
  assert.equal(isWhitelistEnabled('true', 0), true);
  assert.equal(isWhitelistEnabled('TRUE', 0), true);
  assert.equal(isWhitelistEnabled('  True  ', 0), true);
  assert.equal(isWhitelistEnabled('1', 0), true);
  assert.equal(isWhitelistEnabled('yes', 0), true);
  assert.equal(isWhitelistEnabled('YES', 0), true);
  assert.equal(isWhitelistEnabled('on', 0), true);
  assert.equal(isWhitelistEnabled('ON', 0), true);

  // Falsy values must strictly disable even if whitelist has groups
  assert.equal(isWhitelistEnabled('false', 5), false);
  assert.equal(isWhitelistEnabled('FALSE', 5), false);
  assert.equal(isWhitelistEnabled('  False  ', 5), false);
  assert.equal(isWhitelistEnabled('0', 5), false);
  assert.equal(isWhitelistEnabled('no', 5), false);
  assert.equal(isWhitelistEnabled('NO', 5), false);
  assert.equal(isWhitelistEnabled('off', 5), false);
  assert.equal(isWhitelistEnabled('OFF', 5), false);

  // Unset / empty fallback to groupCount > 0
  assert.equal(isWhitelistEnabled(undefined, 2), true);
  assert.equal(isWhitelistEnabled(undefined, 0), false);
  assert.equal(isWhitelistEnabled('', 3), true);
  assert.equal(isWhitelistEnabled('', 0), false);
  assert.equal(isWhitelistEnabled('   ', 1), true);
  assert.equal(isWhitelistEnabled('   ', 0), false);
  assert.equal(isWhitelistEnabled('other_value', 2), true);
});

test('groupIdFromSessionId extracts bare IDs for QQ/OneBot and platform-qualified IDs for non-QQ', () => {
  // QQ / OneBot family -> bare ID
  assert.equal(groupIdFromSessionId('group:12345'), '12345');
  assert.equal(groupIdFromSessionId('GROUP:12345'), '12345');
  assert.equal(groupIdFromSessionId('onebot:group:67890'), '67890');
  assert.equal(groupIdFromSessionId('qq:group:112233'), '112233');
  assert.equal(groupIdFromSessionId('aiocqhttp:group:998877'), '998877');

  // Non-QQ platforms -> platform-qualified
  assert.equal(groupIdFromSessionId('telegram:group:555'), 'telegram:555');
  assert.equal(groupIdFromSessionId('discord:group:777'), 'discord:777');
  assert.equal(groupIdFromSessionId('astrbot:group:888'), 'astrbot:888');

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
  assert.equal(formatGroupOption('telegram:999', 'TG群'), 'telegram:999（TG群）');
});

test('ProactiveWhitelistSync failure path restores actual server state on toggle enable/disable failure', async () => {
  let reloadCalled = false;
  let caughtError: Error | null = null;
  let simulatedFailKey = '';

  const mockApi: WhitelistAPI = {
    async updateEnvVar({ key }) {
      if (key === simulatedFailKey) {
        return { success: false, error: 'Write failed: permission denied' };
      }
      return { success: true };
    },
  };

  const sync = new ProactiveWhitelistSync(
    mockApi,
    { enabled: false, groups: ['101'] },
    {
      onStateChange: () => {},
      onError: (err) => {
        caughtError = err;
      },
      onSuccess: () => {},
      onReloadNeeded: async () => {
        reloadCalled = true;
        // Simulate loadData restoring server state
        const seq = sync.nextLoadSeq();
        sync.applyServerConfig(false, ['101'], seq);
      },
    },
  );

  // 1. Enabling fails
  simulatedFailKey = 'ENABLE_PROACTIVE_REPLY_WHITELIST';
  await sync.toggleEnabled(true);

  assert.ok(caughtError);
  assert.match((caughtError as Error).message, /permission denied/);
  assert.equal(reloadCalled, true);
  // Authoritative server state restored to disabled
  assert.equal(sync.getState().enabled, false);
  assert.equal(sync.getState().isSaving, false);

  // 2. Disabling fails when currently enabled
  reloadCalled = false;
  caughtError = null;
  const seq2 = sync.nextLoadSeq();
  sync.applyServerConfig(true, ['101'], seq2);
  assert.equal(sync.getState().enabled, true);

  const syncForDisable = new ProactiveWhitelistSync(
    mockApi,
    { enabled: true, groups: ['101'] },
    {
      onStateChange: () => {},
      onError: (err) => {
        caughtError = err;
      },
      onSuccess: () => {},
      onReloadNeeded: async () => {
        reloadCalled = true;
        const seq = syncForDisable.nextLoadSeq();
        syncForDisable.applyServerConfig(true, ['101'], seq);
      },
    },
  );

  await syncForDisable.toggleEnabled(false);
  assert.ok(caughtError);
  assert.equal(reloadCalled, true);
  // Authoritative server state restored to enabled
  assert.equal(syncForDisable.getState().enabled, true);
  assert.equal(syncForDisable.getState().isSaving, false);
});

test('ProactiveWhitelistSync serializes rapid concurrent removals and prevents resurrecting groups', async () => {
  const callLog: string[] = [];
  let resolveFirstSave: (() => void) | null = null;

  const mockApi: WhitelistAPI = {
    async updateEnvVar({ key, value }) {
      callLog.push(`${key}=${value}`);
      if (value === 'B') {
        // Slow network on first remove of A (resulting in ['B'])
        await new Promise<void>((resolve) => {
          resolveFirstSave = resolve;
        });
      }
      return { success: true };
    },
  };

  const sync = new ProactiveWhitelistSync(
    mockApi,
    { enabled: true, groups: ['A', 'B'] },
    {
      onStateChange: () => {},
      onError: () => {},
      onSuccess: () => {},
      onReloadNeeded: async () => {},
    },
  );

  // User clicks remove A
  const p1 = sync.removeGroup('A');

  // While remove A is pending in network, user immediately clicks remove B
  const p2 = sync.removeGroup('B');

  let resolver: (() => void) | null = null;
  while (!(resolver = resolveFirstSave)) {
    await new Promise((r) => setImmediate(r));
  }

  // First call in flight is saving ['B']
  assert.equal(callLog[0], 'PROACTIVE_REPLY_GROUP_WHITELIST=B');
  assert.equal(callLog.length, 1);

  // Finish first write
  if (resolver) {
    (resolver as () => void)();
  }
  await Promise.all([p1, p2]);

  // Second write must have run with BOTH A and B removed, saving empty whitelist
  assert.deepEqual(callLog, [
    'PROACTIVE_REPLY_GROUP_WHITELIST=B',
    'PROACTIVE_REPLY_GROUP_WHITELIST=',
  ]);

  // Final state is completely empty; neither A nor B is resurrected
  assert.deepEqual(sync.getState().groups, []);
  assert.equal(sync.getState().isSaving, false);
});

test('ProactiveWhitelistSync rejects out-of-order stale load responses and in-flight overrides', async () => {
  const sync = new ProactiveWhitelistSync(
    {
      async updateEnvVar() {
        return { success: true };
      },
    },
    { enabled: false, groups: [] },
    {
      onStateChange: () => {},
      onError: () => {},
      onSuccess: () => {},
      onReloadNeeded: async () => {},
    },
  );

  const seq1 = sync.nextLoadSeq(); // 1
  const seq2 = sync.nextLoadSeq(); // 2

  // Newer load finishes first
  const ok2 = sync.applyServerConfig(true, ['101', '102'], seq2);
  assert.equal(ok2, true);
  assert.equal(sync.getState().enabled, true);
  assert.deepEqual(sync.getState().groups, ['101', '102']);

  // Stale older load arrives later
  const ok1 = sync.applyServerConfig(false, ['999'], seq1);
  assert.equal(ok1, false);
  // State remains intact from seq2
  assert.equal(sync.getState().enabled, true);
  assert.deepEqual(sync.getState().groups, ['101', '102']);
});

test('ProactiveWhitelistSync rejects pre-edit loads arriving after mutation completes', async () => {
  const sync = new ProactiveWhitelistSync(
    {
      async updateEnvVar() {
        return { success: true };
      },
    },
    { enabled: false, groups: ['100'] },
    {
      onStateChange: () => {},
      onError: () => {},
      onSuccess: () => {},
      onReloadNeeded: async () => {},
    },
  );

  // 1. GET initiated before edit
  const preEditSeq = sync.nextLoadSeq();

  // 2. User mutates
  await sync.addGroup('200');
  assert.deepEqual(sync.getState().groups, ['100', '200']);

  // 3. Pre-edit GET arrives
  const applied = sync.applyServerConfig(false, ['100'], preEditSeq);
  assert.equal(applied, false, 'Pre-edit load must be rejected');
  assert.deepEqual(sync.getState().groups, ['100', '200']);
});
