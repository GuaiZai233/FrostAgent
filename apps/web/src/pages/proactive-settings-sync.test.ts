import test from 'node:test';
import assert from 'node:assert/strict';
import {
  ProactiveSettingsSync,
  type ProactiveAPI,
  type ProactiveState,
} from './proactive-settings-sync.ts';

test('ProactiveSettingsSync serializes rapid Toggle ON -> Toggle OFF ensuring last intent wins', async () => {
  const callLog: string[] = [];
  let resolveFirstEn: (() => void) | null = null;

  const mockApi: ProactiveAPI = {
    async updateEnvVar({ key, value }) {
      callLog.push(`${key}=${value}`);
      if (key === 'ENABLE_PROACTIVE_REPLY' && value === 'true') {
        // simulate slow network for first enable
        await new Promise<void>((resolve) => {
          resolveFirstEn = resolve;
        });
      }
      return { success: true };
    },
  };

  const sync = new ProactiveSettingsSync(
    mockApi,
    { enabled: false, probability: 0.05 },
    {
      onStateChange: () => {},
      onError: () => {},
      onSuccess: () => {},
      onReloadNeeded: async () => {},
    },
  );

  // User toggles ON
  const p1 = sync.setTarget(true, 0.05);

  // User immediately toggles OFF while ON is still pending in network
  const p2 = sync.setTarget(false, 0.05);

  // Wait until the slow first enable call is reached
  while (!resolveFirstEn) {
    await new Promise((r) => setImmediate(r));
  }

  // Check that only the first write (prob and start of enable) has started
  assert.equal(callLog[0], 'PROACTIVE_REPLY_PROBABILITY=0.05');
  assert.equal(callLog[1], 'ENABLE_PROACTIVE_REPLY=true');
  assert.equal(callLog.length, 2);

  // Finish the first slow enable call
  const finishFirstEn: () => void = resolveFirstEn;
  finishFirstEn();
  await Promise.all([p1, p2]);

  // Now the OFF mutation must have run sequentially AFTER the ON mutation
  assert.deepEqual(callLog, [
    'PROACTIVE_REPLY_PROBABILITY=0.05',
    'ENABLE_PROACTIVE_REPLY=true',
    'ENABLE_PROACTIVE_REPLY=false',
  ]);

  // Final state is disabled
  assert.equal(sync.getState().enabled, false);
  assert.equal(sync.getState().isSaving, false);
});

test('ProactiveSettingsSync collapses rapid intermediate slider updates to latest intent', async () => {
  const callLog: string[] = [];
  let resolveFirstProb: (() => void) | null = null;

  const mockApi: ProactiveAPI = {
    async updateEnvVar({ key, value }) {
      callLog.push(`${key}=${value}`);
      if (value === '0.10') {
        await new Promise<void>((resolve) => {
          resolveFirstProb = resolve;
        });
      }
      return { success: true };
    },
  };

  const sync = new ProactiveSettingsSync(
    mockApi,
    { enabled: true, probability: 0.05 },
    {
      onStateChange: () => {},
      onError: () => {},
      onSuccess: () => {},
      onReloadNeeded: async () => {},
    },
  );

  const p1 = sync.setTarget(true, 0.1);
  // User rapidly scrubs through 0.2 to 0.3
  const p2 = sync.setTarget(true, 0.2);
  const p3 = sync.setTarget(true, 0.3);

  resolveFirstProb!();
  await Promise.all([p1, p2, p3]);

  // Notice: 0.2 should be skipped because 0.3 replaced it as the pending target before 0.1 finished!
  assert.deepEqual(callLog, [
    'PROACTIVE_REPLY_PROBABILITY=0.10',
    'ENABLE_PROACTIVE_REPLY=true',
    'PROACTIVE_REPLY_PROBABILITY=0.30',
    'ENABLE_PROACTIVE_REPLY=true',
  ]);
  assert.equal(sync.getState().probability, 0.3);
});

test('ProactiveSettingsSync calls onReloadNeeded on partial or total API failure and restores real server state', async () => {
  let reloadCalled = false;
  let errorCaught: Error | null = null;
  const renderedStates: Array<ProactiveState & { isSaving: boolean }> = [];

  const mockApi: ProactiveAPI = {
    async updateEnvVar({ key }) {
      if (key === 'ENABLE_PROACTIVE_REPLY') {
        return { success: false, error: 'Database locked' };
      }
      return { success: true };
    },
  };

  let sync!: ProactiveSettingsSync;
  sync = new ProactiveSettingsSync(
    mockApi,
    { enabled: false, probability: 0.05 },
    {
      onStateChange: (state) => {
        renderedStates.push({ ...state });
      },
      onError: (err) => {
        errorCaught = err;
      },
      onSuccess: () => {},
      onReloadNeeded: async () => {
        reloadCalled = true;
        // In real backend-settings.ts, onReloadNeeded invokes loadData() which applies actual server values
        const seq = sync.nextLoadSeq();
        const applied = sync.applyServerConfig(false, 0.05, seq);
        assert.equal(applied, true, 'recovery loadData config must be accepted by applyServerConfig');
      },
    },
  );

  await sync.setTarget(true, 0.05);

  assert.ok(errorCaught);
  assert.match((errorCaught as Error).message, /Database locked/);
  assert.equal(reloadCalled, true);
  // Authoritative server state is restored, not stuck on optimistic enabled=true
  assert.equal(sync.getState().enabled, false);
  assert.equal(sync.getState().probability, 0.05);
  assert.equal(sync.getState().isSaving, false);
  assert.ok(renderedStates.length > 0);
  const lastRenderedState = renderedStates[renderedStates.length - 1];
  assert.equal(lastRenderedState.enabled, false);
  assert.equal(lastRenderedState.isSaving, false);
});

test('applyServerConfig rejects out-of-order stale load responses and in-flight overrides', async () => {
  const sync = new ProactiveSettingsSync(
    {
      async updateEnvVar() {
        return { success: true };
      },
    },
    { enabled: false, probability: 0.05 },
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
  const ok2 = sync.applyServerConfig(true, 0.5, seq2);
  assert.equal(ok2, true);
  assert.equal(sync.getState().enabled, true);
  assert.equal(sync.getState().probability, 0.5);

  // Stale older load arrives later
  const ok1 = sync.applyServerConfig(false, 0.01, seq1);
  assert.equal(ok1, false);
  // State remains from seq2
  assert.equal(sync.getState().enabled, true);
  assert.equal(sync.getState().probability, 0.5);
});

test('deferred GET started prior to save or during save cannot revert successful state after save completes', async () => {
  const sync = new ProactiveSettingsSync(
    {
      async updateEnvVar() {
        return { success: true };
      },
    },
    { enabled: false, probability: 0.05 },
    {
      onStateChange: () => {},
      onError: () => {},
      onSuccess: () => {},
      onReloadNeeded: async () => {},
    },
  );

  // 1. A GET is initiated prior to user mutation (e.g. initial loadData)
  const preEditGetSeq = sync.nextLoadSeq();

  // 2. User sets target and save completes successfully
  await sync.setTarget(true, 0.5);
  assert.equal(sync.getState().enabled, true);
  assert.equal(sync.getState().probability, 0.5);
  assert.equal(sync.getState().isSaving, false);

  // 3. Stale pre-edit GET resolves after save
  const appliedPreEdit = sync.applyServerConfig(false, 0.01, preEditGetSeq);
  assert.equal(appliedPreEdit, false, 'Pre-edit GET must be rejected after save completes');
  assert.equal(sync.getState().enabled, true);
  assert.equal(sync.getState().probability, 0.5);

  // 4. A GET initiated DURING save also cannot revert state after save
  let resolveSave: (() => void) | null = null;
  const slowApi: ProactiveAPI = {
    async updateEnvVar({ key }) {
      if (key === 'PROACTIVE_REPLY_PROBABILITY') {
        await new Promise<void>((r) => {
          resolveSave = r;
        });
      }
      return { success: true };
    },
  };
  const sync2 = new ProactiveSettingsSync(
    slowApi,
    { enabled: false, probability: 0.05 },
    {
      onStateChange: () => {},
      onError: () => {},
      onSuccess: () => {},
      onReloadNeeded: async () => {},
    },
  );

  const savePromise = sync2.setTarget(true, 0.8);
  while (!resolveSave) {
    await new Promise((r) => setImmediate(r));
  }
  // GET initiated while save is in-flight
  const duringSaveGetSeq = sync2.nextLoadSeq();

  // Save completes
  const finishSave: () => void = resolveSave;
  finishSave();
  await savePromise;
  assert.equal(sync2.getState().enabled, true);
  assert.equal(sync2.getState().probability, 0.8);
  assert.equal(sync2.getState().isSaving, false);

  // GET initiated during save arrives now
  const appliedDuringSave = sync2.applyServerConfig(false, 0.01, duringSaveGetSeq);
  assert.equal(appliedDuringSave, false, 'GET initiated during save must be rejected after save completes');
  assert.equal(sync2.getState().enabled, true);
  assert.equal(sync2.getState().probability, 0.8);

  // 5. Fresh GET initiated AFTER save completes IS accepted
  const postSaveGetSeq = sync2.nextLoadSeq();
  const appliedPostSave = sync2.applyServerConfig(true, 0.8, postSaveGetSeq);
  assert.equal(appliedPostSave, true, 'GET initiated after save completes must be accepted');
  assert.equal(sync2.getState().enabled, true);
  assert.equal(sync2.getState().probability, 0.8);
});

test('ProactiveSettingsSync clamps probability within [0.01, 1.00]', () => {
  assert.equal(ProactiveSettingsSync.clampProb(0), 0.01);
  assert.equal(ProactiveSettingsSync.clampProb(-0.5), 0.01);
  assert.equal(ProactiveSettingsSync.clampProb(0.004), 0.01);
  assert.equal(ProactiveSettingsSync.clampProb(0.056), 0.06);
  assert.equal(ProactiveSettingsSync.clampProb(1.5), 1.0);
  assert.equal(ProactiveSettingsSync.clampProb(NaN), 0.01);
});
