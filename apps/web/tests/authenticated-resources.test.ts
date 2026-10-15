import { strict as assert } from 'node:assert';
import { setImmediate } from 'node:timers/promises';
import { mountAuthenticatedResources } from '../src/components/authenticated-resources';

let observe: (() => void) | undefined;
const preview = { href: '#' };
const image = {
  dataset: { authSrc: '/instances/a1b2c3d4/api/log-images/synthetic-image' },
  isConnected: true, src: '', alt: '',
  closest: () => preview,
};
let visible = [image];
Object.assign(globalThis, {
  window: { location: { origin: 'https://panel.example' } },
  localStorage: { getItem: () => 'synthetic-control-token' },
  document: { body: {}, addEventListener() {}, querySelectorAll: () => visible },
  MutationObserver: class {
    constructor(callback: () => void) { observe = callback; }
    observe() {}
  },
});
let requests = 0;
let header: string | null = null;
let signal: AbortSignal | undefined;
globalThis.fetch = async (_input: RequestInfo | URL, init?: RequestInit) => {
  requests++;
  header = new Headers(init?.headers).get('Authorization');
  signal = init?.signal ?? undefined;
  return new Response('synthetic-image');
};
let created = 0;
const revoked: string[] = [];
URL.createObjectURL = () => `blob:synthetic-${++created}`;
URL.revokeObjectURL = (url) => { revoked.push(url); };
mountAuthenticatedResources();
observe?.();
await setImmediate();
assert.equal(header, 'Bearer synthetic-control-token');
assert.equal(image.src, 'blob:synthetic-1');
assert.equal(preview.href, image.src);
observe?.();
assert.equal(requests, 1, 'an unchanged image was fetched again');
image.isConnected = false;
visible = [];
observe?.();
assert.equal(signal?.aborted, true);
assert.deepEqual(revoked, ['blob:synthetic-1']);

const pendingImage = { ...image, isConnected: true, src: '' };
visible = [pendingImage];
let resolve: ((response: Response) => void) | undefined;
globalThis.fetch = async (_input: RequestInfo | URL, init?: RequestInit) => {
  signal = init?.signal ?? undefined;
  return new Promise<Response>((done) => { resolve = done; });
};
observe?.();
pendingImage.isConnected = false;
visible = [];
observe?.();
resolve?.(new Response('late-image'));
await setImmediate();
assert.equal(signal?.aborted, true);
assert.equal(created, 1, 'a removed image allocated a stale blob URL');
assert.equal(pendingImage.src, '');
console.log('PASS: protected image fetches carry authorization and release their URLs when removed');
