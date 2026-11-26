import assert from 'node:assert/strict';
import { instanceState } from '../src/instance-state';
import { createTestDOM, query, settle } from './dom-fixture';

const dom = createTestDOM('http://panel.example/#/overview');
const app = dom.root();
app.id = 'app';
dom.window.matchMedia = () =>
  ({ matches: false, addEventListener() {} }) as unknown as MediaQueryList;
const previousObserver = Object.getOwnPropertyDescriptor(
  globalThis,
  'MutationObserver',
);
Object.defineProperty(globalThis, 'MutationObserver', {
  configurable: true,
  value: dom.window.MutationObserver,
});

const key = 'frostagent_selected_instance';
const storage = dom.window.sessionStorage;
const other = {
  id: 'fixture-a',
  name: 'Other instance',
  enabled: true,
  created_at: '',
};
const remembered = {
  ...other,
  id: 'fixture-b',
  name: 'Remembered instance',
  enabled: false,
};
let listedInstances = [other, remembered];
let listResponse: Promise<Response> | undefined;
const requests: string[] = [];
const instanceList = () =>
  Response.json({ instances: listedInstances, next_name: 'Next' });
globalThis.fetch = async (input) => {
  const path = new URL(
    input instanceof Request ? input.url : String(input),
    dom.window.location.origin,
  ).pathname;
  requests.push(path);
  if (path === '/api/instances') return listResponse ?? instanceList();
  if (path === '/api/version')
    return Response.json({ display: 'synthetic-version' });
  if (path.endsWith('/GetOverview'))
    return Response.json({ botName: 'Fixture bot' });
  throw new Error(`Unexpected request: ${path}`);
};

try {
  await instanceState.restoreSelection();
  assert.deepEqual(
    requests,
    [],
    'startup without a saved selection must skip the instance list',
  );

  // The new page starts with an empty in-memory selection and the tab's stored ID.
  storage.setItem(key, remembered.id);
  await settle();
  await import('../src/main');
  document.dispatchEvent(new Event('DOMContentLoaded'));
  await settle();

  assert.deepEqual(instanceState.selected, remembered);
  const labels = app.querySelectorAll('.instance-selected-label');
  assert.equal(labels.length, 2);
  for (const label of labels) {
    assert.match(label.textContent || '', /Remembered instance/);
  }
  assert.match(
    query(app, '#overview-instance').textContent || '',
    /Remembered instance/,
  );
  assert.ok(
    requests.some(
      (path) =>
        path.startsWith(`/instances/${remembered.id}/`) &&
        path.endsWith('/GetOverview'),
    ),
  );
  assert.equal(dom.window.location.hash, '#/overview');

  dom.window.location.hash = '#/settings';
  dom.window.dispatchEvent(new Event('hashchange'));
  instanceState.select(other);
  assert.equal(storage.getItem(key), other.id);
  instanceState.select(null);
  assert.equal(storage.getItem(key), null);

  for (const instances of [
    [other],
    [other, { ...remembered, deleting: true }],
  ]) {
    listedInstances = instances;
    storage.setItem(key, remembered.id);
    await instanceState.restoreSelection();
    assert.equal(instanceState.selected, null);
    assert.equal(
      storage.getItem(key),
      null,
      'unavailable instances must not be remembered',
    );
  }

  listedInstances = [other, remembered];
  storage.setItem(key, remembered.id);
  listResponse = Promise.resolve(
    Response.json({ error: 'unavailable' }, { status: 503 }),
  );
  await assert.rejects(instanceState.restoreSelection(), /unavailable/);
  assert.equal(instanceState.selected, null);
  assert.equal(
    storage.getItem(key),
    remembered.id,
    'a failed request must preserve the saved ID',
  );

  let resolveList!: (response: Response) => void;
  listResponse = new Promise((resolve) => {
    resolveList = resolve;
  });
  const pending = instanceState.restoreSelection();
  instanceState.select(other);
  resolveList(instanceList());
  await pending;
  assert.deepEqual(instanceState.selected, other);
  assert.equal(
    storage.getItem(key),
    other.id,
    'a late restoration must respect a manual selection',
  );
} finally {
  dom.cleanup();
  if (previousObserver)
    Object.defineProperty(globalThis, 'MutationObserver', previousObserver);
  else Reflect.deleteProperty(globalThis, 'MutationObserver');
}

console.log(
  'PASS: panel reload restores a verified instance and respects newer selections',
);
