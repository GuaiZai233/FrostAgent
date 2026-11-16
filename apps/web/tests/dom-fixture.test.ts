import assert from 'node:assert/strict';
import { createTestDOM, emit, query } from './dom-fixture';

const before = Object.getOwnPropertyDescriptor(globalThis, 'document');
const originalFetch = globalThis.fetch;
const dom = createTestDOM();
try {
  const root = dom.root();
  root.innerHTML =
    '<section><input id="number" type="number" min="2" step="1"><button disabled>Save</button></section>';
  assert.equal(root.querySelectorAll('section input').length, 1);
  assert.equal(root.querySelector('#missing'), null);
  assert.throws(() => query(root, '#missing'), /missing element/);
  const input = query<HTMLInputElement>(root, '#number');
  input.value = '1';
  assert.equal(input.reportValidity(), false);
  input.value = '2';
  assert.equal(input.reportValidity(), true);
  input.value = 'invalid';
  assert.equal(input.value, '', 'numeric sanitation must come from the DOM');
  let events = 0;
  root.addEventListener('input', () => events++);
  emit(input, 'input');
  assert.equal(events, 1, 'events must bubble through real parents');
  const controller = new AbortController();
  const request = new Request('http://localhost/', {
    signal: controller.signal,
  });
  let changes = 0;
  input.addEventListener('change', () => changes++, {
    signal: controller.signal,
  });
  emit(input, 'change');
  controller.abort();
  emit(input, 'change');
  assert.equal(changes, 1, 'aborting Node signals must remove DOM listeners');
  assert.equal(
    request.signal.aborted,
    true,
    'Node requests must retain signal compatibility',
  );
  input.addEventListener('change', () => changes++, {
    signal: controller.signal,
  });
  emit(input, 'change');
  assert.equal(
    changes,
    1,
    'already aborted signals must not register listeners',
  );
  const button = query<HTMLButtonElement>(root, 'button');
  button.addEventListener('click', () => events++);
  button.click();
  assert.equal(events, 1, 'disabled controls must not activate');
  const dialog = document.createElement('dialog');
  root.appendChild(dialog);
  dialog.showModal();
  assert.equal(dom.dialogs.at(-1), dialog);
  assert.equal(dialog.open, true);
  dialog.close();
  assert.equal(dialog.open, false);
  globalThis.fetch = async () => Response.json({ fixture: true });
} finally {
  dom.cleanup();
}
dom.cleanup();
assert.deepEqual(
  Object.getOwnPropertyDescriptor(globalThis, 'document'),
  before,
);
assert.equal(globalThis.fetch, originalFetch);
const failingDOM = createTestDOM();
try {
  const button = failingDOM.root();
  button.addEventListener('click', () => { throw new Error('synthetic listener failure'); });
  button.click();
  assert.throws(() => failingDOM.cleanup(), /synthetic listener failure/);
  assert.deepEqual(Object.getOwnPropertyDescriptor(globalThis, 'document'), before);
} finally {
  failingDOM.cleanup();
}
console.log(
  'PASS: shared DOM fixture preserves native behavior and restores globals',
);
