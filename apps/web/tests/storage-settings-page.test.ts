import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers/promises';
import { mountStorageSettingsPage } from '../src/pages/storage-settings';
import { toast } from '../src/components/toast';

class Element {
  innerHTML = '';
  value = '';
  hidden = false;
  disabled = false;
  onchange: (() => void) | null = null;
  onclick: (() => Promise<void>) | null = null;
  private elements = new Map<string, Element>();
  querySelector(selector: string): Element {
    let element = this.elements.get(selector);
    if (!element) {
      element = new Element();
      this.elements.set(selector, element);
    }
    return element;
  }
}

Object.assign(globalThis, {
  window: Object.assign(new EventTarget(), { location: { origin: 'https://panel.example' } }),
  document: { querySelectorAll: () => [] },
});
const errors: string[] = [];
toast.error = (message) => { errors.push(message); };
toast.success = () => {};
const address = 'postgres://synthetic:synthetic@localhost/synthetic';
let saved = { backend: 'postgres', dsn: address };
const writes: typeof saved[] = [];
globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  if (String(input) === '/api/instances') {
    return Response.json({ instances: [], next_name: '实例1' });
  }
  assert.equal(String(input), '/api/storage');
  if (init?.method === 'POST') {
    const change = JSON.parse(String(init.body)) as typeof saved;
    writes.push(change);
    saved = change;
    return Response.json({ success: true });
  }
  return Response.json(saved);
};

let root = new Element();
let cleanup = mountStorageSettingsPage(root as unknown as HTMLElement);
await setImmediate();
const select = (backend: string) => {
  root.querySelector('#storage-backend').value = backend;
  root.querySelector('#storage-backend').onchange?.();
};
assert.equal(root.querySelector('#storage-dsn').value, address);
select('sqlite');
assert.equal(root.querySelector('#storage-dsn-group').hidden, true);
select('postgres');
assert.equal(root.querySelector('#storage-dsn').value, address);
const edited = 'postgres://synthetic:edited@localhost/synthetic';
root.querySelector('#storage-dsn').value = edited;
select('sqlite');
await root.querySelector('#storage-save').onclick?.();
assert.deepEqual(writes.at(-1), { backend: 'sqlite', dsn: edited }, 'SQLite save cleared the entered PostgreSQL address');

cleanup();
root = new Element();
cleanup = mountStorageSettingsPage(root as unknown as HTMLElement);
await setImmediate();
assert.equal(root.querySelector('#storage-backend').value, 'sqlite');
assert.equal(root.querySelector('#storage-dsn-group').hidden, true);
select('postgres');
assert.equal(root.querySelector('#storage-dsn').value, edited, 'remount lost the saved PostgreSQL address');
await root.querySelector('#storage-save').onclick?.();
assert.deepEqual(writes.at(-1), { backend: 'postgres', dsn: edited });
assert.deepEqual(errors, []);
cleanup();
console.log('PASS: PostgreSQL address survives SQLite selection, saving, page remount, and switching back');
