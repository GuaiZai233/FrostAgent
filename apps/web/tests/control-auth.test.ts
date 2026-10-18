import { strict as assert } from 'node:assert';
import { controlPlaneFetch, downloadControlPlaneFile, setControlToken } from '../src/api/control-auth';
import { instanceRequest, restoreInstanceZIP } from '../src/instance-state';
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

const archive = new Blob(['synthetic ZIP contents'], { type: 'application/zip' });
const restored = { id: 'a1b2c3d4', name: 'Imported', enabled: false, created_at: '' };
let uploadBody: BodyInit | null | undefined;
globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  calls.push({ url: String(input), header: new Headers(init?.headers).get('Authorization'), method: init?.method });
  uploadBody = init?.body;
  return new Response(JSON.stringify(restored));
};
assert.deepEqual(await restoreInstanceZIP(archive), restored);
assert.equal(calls.at(-1)?.url, '/api/instances/restore');
assert.equal(calls.at(-1)?.header, 'Bearer synthetic-control-token');
assert.equal(calls.at(-1)?.method, 'POST');
assert.equal(uploadBody, archive, 'backup ZIP was not uploaded as the request body');

let clicked = false;
let downloaded = '';
let released = false;
let release: (() => void) | undefined;
const link = {
  href: '', download: '',
  click() { clicked = true; downloaded = this.download; },
  remove() {},
};
Object.assign(globalThis, {
  document: { body: { appendChild() {} }, createElement: () => link },
  setTimeout: (callback: () => void) => { release = callback; return 0; },
});
URL.createObjectURL = () => 'blob:synthetic-backup';
URL.revokeObjectURL = () => { released = true; };
await downloadControlPlaneFile('/api/instances/a1b2c3d4/backup', 'backup.zip');
assert.equal(calls.at(-1)?.header, 'Bearer synthetic-control-token');
assert.equal(clicked, true);
assert.equal(downloaded, 'backup.zip');
assert.equal(released, false, 'download URL released before the browser received it');
release?.();
assert.equal(released, true);

globalThis.fetch = async () => new Response('unauthorized', { status: 401 });
await assert.rejects(instanceRequest(''), /前端设置/);
console.log('PASS: remote management requests and backup downloads carry the token without external disclosure');
