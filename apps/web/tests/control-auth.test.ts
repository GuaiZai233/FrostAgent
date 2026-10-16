import { strict as assert } from 'node:assert';
import { setImmediate } from 'node:timers/promises';
import { controlPlaneFetch, downloadControlPlaneFile, setControlToken } from '../src/api/control-auth';
import { instanceRequest } from '../src/instance-state';
import { extraEnvAPI, globalSettingsAPI } from '../src/api/client';

const storage = new Map<string, string>();
Object.assign(globalThis, {
  window: { location: { origin: 'https://panel.example' } },
  localStorage: {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, value),
    removeItem: (key: string) => storage.delete(key),
  },
});
const calls: { url: string; header: string | null; method?: string }[] = [];
globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  calls.push({ url: String(input), header: new Headers(init?.headers).get('Authorization'), method: init?.method });
  return new Response(JSON.stringify({ success: true, entries: [] }));
};
setControlToken(' synthetic-control-token ');
await instanceRequest('');
await instanceRequest('/global/import/security', {});
await controlPlaneFetch('/api/storage');
await extraEnvAPI.list();
await globalSettingsAPI.download();
await globalSettingsAPI.import({});
assert.equal(calls.length, 6);
assert.ok(calls.every((call) => call.header === 'Bearer synthetic-control-token'));
assert.equal(calls[1].method, 'POST');
assert.equal(calls[5].method, 'POST');
await assert.rejects(controlPlaneFetch('https://external.example/api/storage'), /同源/);
assert.equal(calls.length, 6, 'credentials leaked to an external origin');

let clicked = false;
let downloaded = '';
let released = false;
let release: (() => void) | undefined;
let releaseDelay = 0;
let savedBlob: Blob | undefined;
const link = {
  href: '', download: '',
  click() { clicked = true; downloaded = this.download; },
  remove() {},
};
Object.assign(globalThis, {
  document: { body: { appendChild() {} }, createElement: () => link },
  setTimeout: (callback: () => void, delay: number) => { release = callback; releaseDelay = delay; return 0; },
});
URL.createObjectURL = (blob) => { savedBlob = blob as Blob; return 'blob:synthetic-backup'; };
URL.revokeObjectURL = () => { released = true; };
await downloadControlPlaneFile('/api/instances/a1b2c3d4/backup', 'backup.zip');
assert.equal(calls.at(-1)?.header, 'Bearer synthetic-control-token');
assert.equal(clicked, true);
assert.equal(downloaded, 'backup.zip');
assert.equal(released, false, 'download URL released before the browser received it');
assert.ok(releaseDelay >= 60_000, 'Firefox download URLs must survive delayed save dialogs');
release?.();
assert.equal(released, true);

clicked = false;
savedBlob = undefined;
let body: ReadableStreamDefaultController<Uint8Array> | undefined;
globalThis.fetch = async () => new Response(new ReadableStream<Uint8Array>({ start(controller) { body = controller; } }));
const complete = downloadControlPlaneFile('/api/instances/a1b2c3d4/backup', 'full.zip');
await setImmediate();
body!.enqueue(new Uint8Array([0x50, 0x4b, 0x03, 0x04]));
await setImmediate();
assert.equal(clicked, false, 'download started with an incomplete response');
assert.equal(savedBlob, undefined);
body!.close();
await complete;
assert.equal(clicked, true);
assert.deepEqual(new Uint8Array(await savedBlob!.arrayBuffer()), new Uint8Array([0x50, 0x4b, 0x03, 0x04]));

clicked = false;
savedBlob = undefined;
globalThis.fetch = async () => new Response(new Uint8Array());
await assert.rejects(downloadControlPlaneFile('/api/instances/a1b2c3d4/backup', 'empty.zip'), /文件为空/);
assert.equal(clicked, false, 'empty response created a zero-byte download');
assert.equal(savedBlob, undefined);
globalThis.fetch = async () => new Response('synthetic backup failure', { status: 500 });
await assert.rejects(downloadControlPlaneFile('/api/instances/a1b2c3d4/backup', 'failed.zip'), /synthetic backup failure/);
assert.equal(clicked, false, 'HTTP error was saved as a backup');
globalThis.fetch = async () => new Response(new ReadableStream({ start(controller) { controller.error(new Error('synthetic interrupted body')); } }));
await assert.rejects(downloadControlPlaneFile('/api/instances/a1b2c3d4/backup', 'interrupted.zip'), /interrupted body/);
assert.equal(clicked, false, 'interrupted response was saved as a backup');

globalThis.fetch = async () => new Response('unauthorized', { status: 401 });
await assert.rejects(instanceRequest(''), /前端设置/);
console.log('PASS: remote management requests and backup downloads carry the token without external disclosure');
