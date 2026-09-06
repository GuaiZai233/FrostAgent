import { strict as assert } from 'node:assert';
import { createInstanceAPI } from '../src/api/client';
import { instanceState } from '../src/instance-state';
const events = new EventTarget();
Object.assign(globalThis, {
  window: Object.assign(events, { location: { origin: 'http://localhost' } }),
  document: { querySelectorAll: () => [] },
});
const calls: string[] = [];
globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = input instanceof Request ? input.url : String(input);
  init?.signal?.throwIfAborted();
  calls.push(url);
  return new Response(JSON.stringify({ success: true }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
};
const a = { id: 'a1b2c3d4', name: 'a', enabled: false, created_at: '' };
const b = { id: 'b1c2d3e4', name: 'b', enabled: false, created_at: '' };
instanceState.select(a);
const oldPage = createInstanceAPI();
// Simulates deferred FileReader/arrayBuffer continuations and the next item of
// a batch that resumes after the user switches instance.
const delayedUpload = async () => {
  await Promise.resolve();
  await oldPage.uploadSticker(new Uint8Array([1, 2, 3]), 'synthetic.png');
};
const pending = delayedUpload();
instanceState.select(b);
await assert.rejects(pending);
await assert.rejects(oldPage.importMemories('{"entries":[]}', false));
await assert.rejects(oldPage.uploadSticker(new Uint8Array([1]), 'second.png'));
assert.equal(calls.length, 0, 'stale page transmitted data after a switch');
await createInstanceAPI().updateEnvVar({
  key: 'BOT_NAME',
  value: 'b',
  isSecret: false,
});
assert.equal(calls.length, 1);
assert.ok(calls[0].includes('/instances/b1c2d3e4/'));
console.log(
  'PASS: stale upload/import continuations cannot target a new instance',
);
