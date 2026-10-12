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
  assert.equal(isWhitelistEnabled('TrUe', 0), true);

  // Falsy values must strictly disable even if whitelist has groups
  assert.equal(isWhitelistEnabled('false', 5), false);
  assert.equal(isWhitelistEnabled('FALSE', 5), false);
  assert.equal(isWhitelistEnabled('  False  ', 5), false);
  assert.equal(isWhitelistEnabled('FaLsE', 5), false);

  // Unset / empty or non-boolean fallback to groupCount > 0 (matching Go strings.EqualFold)
  assert.equal(isWhitelistEnabled(undefined, 2), true);
  assert.equal(isWhitelistEnabled(undefined, 0), false);
  assert.equal(isWhitelistEnabled('', 3), true);
  assert.equal(isWhitelistEnabled('', 0), false);
  assert.equal(isWhitelistEnabled('   ', 1), true);
  assert.equal(isWhitelistEnabled('   ', 0), false);
  assert.equal(isWhitelistEnabled('other_value', 2), true);
  assert.equal(isWhitelistEnabled('other_value', 0), false);

  // Non-boolean aliases (1/0/yes/no/on/off) are unsupported by Go and fall back to groupCount > 0 (N3)
  assert.equal(isWhitelistEnabled('1', 2), true);
  assert.equal(isWhitelistEnabled('1', 0), false);
  assert.equal(isWhitelistEnabled('0', 2), true);
  assert.equal(isWhitelistEnabled('0', 0), false);
  assert.equal(isWhitelistEnabled('yes', 2), true);
  assert.equal(isWhitelistEnabled('yes', 0), false);
  assert.equal(isWhitelistEnabled('no', 2), true);
  assert.equal(isWhitelistEnabled('no', 0), false);
  assert.equal(isWhitelistEnabled('on', 2), true);
  assert.equal(isWhitelistEnabled('on', 0), false);
  assert.equal(isWhitelistEnabled('off', 2), true);
  assert.equal(isWhitelistEnabled('off', 0), false);
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
    { enabled: true, groups: ['A', 'B'], explicitSwitch: 'true' },
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

test('ProactiveWhitelistSync persists explicit switch before group list across implicit boundaries (N1 Repro A & B)', async () => {
  // Repro A: switch unset (null), groups=['A'], enabled=true
  // Removing 'A' must write ENABLE_PROACTIVE_REPLY_WHITELIST=true BEFORE PROACTIVE_REPLY_GROUP_WHITELIST=
  const callLogA: string[] = [];
  const mockApiA: WhitelistAPI = {
    async updateEnvVar({ key, value }) {
      callLogA.push(`${key}=${value}`);
      return { success: true };
    },
  };

  const syncA = new ProactiveWhitelistSync(
    mockApiA,
    { enabled: true, groups: ['A'], explicitSwitch: null },
    {
      onStateChange: () => {},
      onError: () => {},
      onSuccess: () => {},
      onReloadNeeded: async () => {},
    },
  );

  await syncA.removeGroup('A');

  assert.deepEqual(callLogA, [
    'ENABLE_PROACTIVE_REPLY_WHITELIST=true',
    'PROACTIVE_REPLY_GROUP_WHITELIST=',
  ]);
  assert.deepEqual(syncA.getState().groups, []);
  assert.equal(syncA.getState().enabled, true);

  // Repro B: switch unset (null), groups=[], enabled=false
  // Adding 'B' while disabled must write ENABLE_PROACTIVE_REPLY_WHITELIST=false BEFORE PROACTIVE_REPLY_GROUP_WHITELIST=B
  const callLogB: string[] = [];
  const mockApiB: WhitelistAPI = {
    async updateEnvVar({ key, value }) {
      callLogB.push(`${key}=${value}`);
      return { success: true };
    },
  };

  const syncB = new ProactiveWhitelistSync(
    mockApiB,
    { enabled: false, groups: [], explicitSwitch: null },
    {
      onStateChange: () => {},
      onError: () => {},
      onSuccess: () => {},
      onReloadNeeded: async () => {},
    },
  );

  await syncB.addGroup('B');

  assert.deepEqual(callLogB, [
    'ENABLE_PROACTIVE_REPLY_WHITELIST=false',
    'PROACTIVE_REPLY_GROUP_WHITELIST=B',
  ]);
  assert.deepEqual(syncB.getState().groups, ['B']);
  assert.equal(syncB.getState().enabled, false);
});

test('ProactiveWhitelistSync immediately restores lastSaved and sets isUnverified on write failure even when reload fails (N4)', async () => {
  let reloadAttempts = 0;
  let reportedError: Error | null = null;

  const mockApi: WhitelistAPI = {
    async updateEnvVar({ key }) {
      if (key === 'PROACTIVE_REPLY_GROUP_WHITELIST') {
        return { success: false, error: 'Network partition' };
      }
      return { success: true };
    },
  };

  const sync = new ProactiveWhitelistSync(
    mockApi,
    { enabled: true, groups: ['100', '200'] },
    {
      onStateChange: () => {},
      onError: (err) => {
        reportedError = err;
      },
      onSuccess: () => {},
      onReloadNeeded: async () => {
        reloadAttempts++;
        throw new Error('Transport down: fetch failed');
      },
    },
  );

  await sync.removeGroup('100');

  // Must have caught the error
  assert.ok(reportedError);
  assert.match((reportedError as Error).message, /Network partition/);
  assert.equal(reloadAttempts, 1);

  // Optimistic edit must be rolled back to lastSaved state immediately
  const state = sync.getState();
  assert.deepEqual(state.groups, ['100', '200'], 'Groups should be rolled back to lastSaved');
  assert.equal(state.enabled, true);
  assert.equal(state.isSaving, false);
  assert.equal(state.isUnverified, true, 'isUnverified flag must be set when write or reload fails');
});

test('ProactiveWhitelistSync forces full reconciliation of both keys on next save after failed write/reload (R3)', async () => {
  const callLog: string[] = [];
  let shouldFailGroupWrite = false;

  const mockApi: WhitelistAPI = {
    async updateEnvVar({ key, value }) {
      callLog.push(`${key}=${value}`);
      if (key === 'PROACTIVE_REPLY_GROUP_WHITELIST' && shouldFailGroupWrite) {
        // Simulate write applied server-side but response lost / network timeout
        return { success: false, error: 'Network timeout (response lost)' };
      }
      return { success: true };
    },
  };

  const sync = new ProactiveWhitelistSync(
    mockApi,
    { enabled: true, groups: ['A', 'B'], explicitSwitch: 'true' },
    {
      onStateChange: () => {},
      onError: () => {},
      onSuccess: () => {},
      onReloadNeeded: async () => {
        // Simulate reload failure (e.g. server unreachable)
        throw new Error('Reload failed: connection reset');
      },
    },
  );

  // 1. User removes 'A'
  shouldFailGroupWrite = true;
  await sync.removeGroup('A');

  // After failed write + failed reload:
  // UI rolled back to ['A', 'B'], isUnverified is true
  assert.equal(sync.getState().isUnverified, true);
  assert.deepEqual(sync.getState().groups, ['A', 'B']);
  assert.equal(sync.getState().enabled, true);
  assert.deepEqual(callLog, ['PROACTIVE_REPLY_GROUP_WHITELIST=B']);

  // 2. User performs an unrelated toggle (toggles switch OFF)
  shouldFailGroupWrite = false;
  await sync.toggleEnabled(false);

  // R3: Because isUnverified was true, executeSave MUST force full reconciliation of BOTH keys:
  // It must write ENABLE_PROACTIVE_REPLY_WHITELIST=false AND PROACTIVE_REPLY_GROUP_WHITELIST=A, B
  assert.deepEqual(callLog, [
    'PROACTIVE_REPLY_GROUP_WHITELIST=B', // original failed write
    'ENABLE_PROACTIVE_REPLY_WHITELIST=false', // switch written
    'PROACTIVE_REPLY_GROUP_WHITELIST=A, B',   // group list reconciled!
  ]);

  // Both keys were reconciled and saved successfully, so isUnverified is now safely cleared
  assert.equal(sync.getState().isUnverified, false);
  assert.equal(sync.getState().enabled, false);
  assert.deepEqual(sync.getState().groups, ['A', 'B']);
});
