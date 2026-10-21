import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers/promises';
import { mountAuthenticatedResources } from '../src/components/authenticated-resources';
import { toast } from '../src/components/toast';

class Target {
  closest() { return button; }
}
class Button extends Target {
  disabled = false;
  dataset = { controlDownload: '/api/instances/a1b2c3d4/backup', downloadFilename: 'full.zip' };
  attributes = new Map<string, string>();
  setAttribute(key: string, value: string) { this.attributes.set(key, value); }
  removeAttribute(key: string) { this.attributes.delete(key); }
}
const button = new Button();
let handleClick: ((event: MouseEvent) => void) | undefined;
let requests = 0;
let header: string | null = null;
let finish: ((response: Response) => void) | undefined;
const saved: { href: string; filename: string }[] = [];
const errors: string[] = [];
toast.error = (error) => errors.push(error);
Object.assign(globalThis, {
  window: { location: { origin: 'https://panel.example' } },
  localStorage: { getItem: () => 'synthetic-control-token' },
  Element: Target,
  HTMLButtonElement: Button,
  MutationObserver: class { observe() {} },
  document: {
    body: { appendChild() {} },
    addEventListener(_type: string, listener: (event: MouseEvent) => void) { handleClick = listener; },
    querySelectorAll: () => [],
    createElement() {
      return {
        href: '', download: '', remove() {},
        click() { saved.push({ href: this.href, filename: this.download }); },
      };
    },
  },
});
URL.createObjectURL = () => 'blob:synthetic-full-backup';
globalThis.fetch = async (_url: RequestInfo | URL, init?: RequestInit) => {
  requests++;
  header = new Headers(init?.headers).get('Authorization');
  return new Promise<Response>((resolve) => { finish = resolve; });
};
mountAuthenticatedResources();
const click = () => {
  let prevented = false;
  handleClick!({ target: new Target(), preventDefault() { prevented = true; } } as unknown as MouseEvent);
  return prevented;
};
assert.equal(click(), true);
assert.equal(button.disabled, true);
assert.equal(button.attributes.get('aria-busy'), 'true');
assert.equal(requests, 1);
assert.equal(header, 'Bearer synthetic-control-token');
assert.equal(click(), true);
assert.equal(requests, 1, 'repeated clicks started duplicate full backups');
assert.deepEqual(saved, [], 'browser was handed the HTTP download before it completed');
finish!(new Response(new Uint8Array([0x50, 0x4b, 0x03, 0x04]), { headers: { 'Content-Type': 'application/zip' } }));
await setImmediate();
assert.deepEqual(saved, [{ href: 'blob:synthetic-full-backup', filename: 'full.zip' }]);
assert.equal(button.disabled, false);
assert.equal(button.attributes.has('aria-busy'), false);

click();
finish!(new Response('synthetic backup failed', { status: 500 }));
await setImmediate();
assert.equal(saved.length, 1, 'failed response created another download');
assert.match(errors.at(-1) ?? '', /synthetic backup failed/);
assert.equal(button.disabled, false, 'failed download could not be retried');

button.dataset.controlDownload = 'https://external.example/api/instances/a1b2c3d4/backup';
assert.equal(click(), false);
assert.equal(requests, 2, 'external download received control-plane credentials');
assert.equal(button.disabled, false);
console.log('PASS: backup buttons authenticate, wait for complete bytes, reject errors, and prevent duplicate downloads');
