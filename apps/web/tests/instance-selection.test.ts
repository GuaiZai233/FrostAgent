import assert from 'node:assert/strict';
import { instanceState } from '../src/instance-state';
import { createTestDOM, emit, query, settle } from './dom-fixture';

const dom = createTestDOM('http://panel.example/');
const app = dom.root();
app.id = 'app';
dom.window.matchMedia = () =>
  ({ matches: false, addEventListener() {} }) as unknown as MediaQueryList;
const globals = ['Element', 'MutationObserver'] as const;
const previousGlobals = globals.map((key) =>
  Object.getOwnPropertyDescriptor(globalThis, key),
);
for (const key of globals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom.window[key],
  });
}

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
function deferInstanceList(): () => void {
  let respond!: (response: Response) => void;
  listResponse = new Promise((resolve) => {
    respond = resolve;
  });
  return () => {
    listResponse = undefined;
    respond(instanceList());
  };
}
const nextHashChange = () =>
  new Promise<void>((resolve) =>
    dom.window.addEventListener('hashchange', () => resolve(), { once: true }),
  );
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
  const initialNavigation = nextHashChange();
  const finishStartup = deferInstanceList();
  document.dispatchEvent(new Event('DOMContentLoaded'));
  await initialNavigation;
  finishStartup();
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

  const settingsNavigation = nextHashChange();
  dom.window.location.hash = '#/settings';
  await settingsNavigation;
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

  const finishAfterSelection = deferInstanceList();
  const pendingSelection = instanceState.restoreSelection();
  instanceState.select(other);
  finishAfterSelection();
  await pendingSelection;
  assert.deepEqual(instanceState.selected, other);
  assert.equal(
    storage.getItem(key),
    other.id,
    'a late restoration must respect a manual selection',
  );

  instanceState.select(null);
  storage.setItem(key, remembered.id);
  const finishAfterNavigation = deferInstanceList();
  const pendingNavigation = instanceState.restoreSelection();
  const navigation = nextHashChange();
  dom.window.location.hash = '#/settings/credentials';
  await navigation;
  const tokenInput = query(app, '#control-token-input');
  finishAfterNavigation();
  await pendingNavigation;
  assert.equal(
    query(app, '#control-token-input'),
    tokenInput,
    'navigation must cancel restoration',
  );
  assert.equal(instanceState.selected, null);

  const finishAfterInput = deferInstanceList();
  const pendingInput = instanceState.restoreSelection();
  tokenInput.value = 'synthetic-unsaved-token';
  emit(tokenInput, 'input');
  finishAfterInput();
  await pendingInput;
  assert.equal(
    query(app, '#control-token-input').value,
    'synthetic-unsaved-token',
  );
  assert.equal(instanceState.selected, null);

  const finishAfterDialog = deferInstanceList();
  const pendingDialog = instanceState.restoreSelection();
  listResponse = undefined;
  query<HTMLButtonElement>(app, '.instance-manager-btn').click();
  await settle();
  const dialog = query<HTMLDialogElement>(document, 'dialog[open]');
  finishAfterDialog();
  await pendingDialog;
  assert.equal(query(document, 'dialog[open]'), dialog);
  assert.equal(instanceState.selected, null);
  assert.equal(
    storage.getItem(key),
    remembered.id,
    'cancellation must preserve the saved ID',
  );
} finally {
  dom.cleanup();
  globals.forEach((key, index) => {
    const descriptor = previousGlobals[index];
    if (descriptor) Object.defineProperty(globalThis, key, descriptor);
    else Reflect.deleteProperty(globalThis, key);
  });
}

console.log(
  'PASS: panel reload restores a verified instance and respects navigation, input, and manual selection',
);
