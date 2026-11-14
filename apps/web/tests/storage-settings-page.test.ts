import { createTestDOM, emit } from './dom-fixture';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers/promises';
import { mountStorageSettingsPage } from '../src/pages/storage-settings';
import { toast } from '../src/components/toast';

const dom = createTestDOM('https://panel.example');
try {
  const errors: string[] = [];
  toast.error = (message) => { errors.push(message); };
  toast.success = () => {};
  const address = 'postgres://synthetic:synthetic@localhost/synthetic';
  let saved = { backend: 'postgres', dsn: address };
  const writes: (typeof saved)[] = [];
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

  let root = dom.root();
  let cleanup = mountStorageSettingsPage(root as unknown as HTMLElement);
  await setImmediate();
  const select = (backend: string) => {
    root.querySelector('#storage-backend').value = backend;
    emit(root.querySelector('#storage-backend'), 'change');
  };
  assert.equal(root.querySelector('#storage-dsn').value, address);
  select('sqlite');
  assert.equal(root.querySelector('#storage-dsn-group').hidden, true);
  select('postgres');
  assert.equal(root.querySelector('#storage-dsn').value, address);
  const edited = 'postgres://synthetic:edited@localhost/synthetic';
  root.querySelector('#storage-dsn').value = edited;
  select('sqlite');
  root.querySelector('#storage-save').click();
  await setImmediate();
  assert.deepEqual(writes.at(-1), { backend: 'sqlite', dsn: edited }, 'SQLite save cleared the entered PostgreSQL address');

  cleanup();
  root = dom.root();
  cleanup = mountStorageSettingsPage(root as unknown as HTMLElement);
  await setImmediate();
  assert.equal(root.querySelector('#storage-backend').value, 'sqlite');
  assert.equal(root.querySelector('#storage-dsn-group').hidden, true);
  select('postgres');
  assert.equal(root.querySelector('#storage-dsn').value, edited, 'remount lost the saved PostgreSQL address');
  root.querySelector('#storage-save').click();
  await setImmediate();
  assert.deepEqual(writes.at(-1), { backend: 'postgres', dsn: edited });
  assert.deepEqual(errors, []);
  cleanup();
  console.log('PASS: PostgreSQL address survives SQLite selection, saving, page remount, and switching back');
} finally {
  dom.cleanup();
}
