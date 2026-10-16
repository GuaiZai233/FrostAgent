import assert from 'node:assert/strict';
import { mountActionsCatPage } from '../src/pages/actionscat';
import { instanceState } from '../src/instance-state';
import { toast } from '../src/components/toast';

class MockElement {
  innerHTML = '';
  textContent = '';
  value = '';
  checked = false;
  disabled = false;
  hidden = false;
  className = '';
  style: Record<string, string> = {};
  dataset: Record<string, string> = {};
  private children = new Map<string, MockElement>();
  private listeners = new Map<string, () => unknown>();

  addEventListener(name: string, listener: () => unknown) { this.listeners.set(name, listener); }
  trigger(name: string) { return this.listeners.get(name)?.(); }
  querySelector(selector: string): MockElement {
    if (!this.children.has(selector)) this.children.set(selector, new MockElement());
    return this.children.get(selector)!;
  }
  querySelectorAll(): MockElement[] { return []; }
}

Object.assign(globalThis, {
  document: { querySelectorAll: () => [] },
  window: Object.assign(new EventTarget(), { location: { origin: 'http://localhost' } }),
});
toast.success = () => {};
instanceState.select({ id: 'test-instance', name: 'Test', enabled: true, created_at: '' });
const calls: string[] = [];
let disabled = true;
globalThis.fetch = async (input: RequestInfo | URL) => {
  const url = input instanceof Request ? input.url : String(input);
  calls.push(url);
  let body: unknown = [];
  if (url.endsWith('/status')) {
    body = { disabled, configured: true, healthy: !disabled, authenticated: !disabled, endpoint: 'http://test.invalid' };
  }
  return new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } });
};
const settle = async () => {
  for (let i = 0; i < 15; i++) await new Promise((resolve) => setTimeout(resolve, 0));
};

const actionsRoot = new MockElement();
const disposeActions = mountActionsCatPage(actionsRoot as unknown as HTMLElement);
await settle();
assert.equal(actionsRoot.querySelector('#actionscat-tools-card').hidden, true);
assert.equal(actionsRoot.querySelector('#actionscat-dispatch-btn').disabled, true);
assert.equal(actionsRoot.querySelector('#actionscat-create-action-btn').disabled, true);
assert.match(actionsRoot.querySelector('#actionscat-status-container').innerHTML, /ActionsCat已被FrostAgent管理员禁用。/);
assert.equal(calls.filter((url) => url.includes('/api/actionscat')).length, 1, 'disabled page must only query status');
disabled = false;
actionsRoot.querySelector('#actionscat-refresh-btn').trigger('click');
await settle();
assert.equal(actionsRoot.querySelector('#actionscat-tools-card').hidden, false);
assert.equal(actionsRoot.querySelector('#actionscat-dispatch-btn').disabled, false);
assert.equal(actionsRoot.querySelector('#actionscat-create-action-btn').disabled, false);
assert.ok(calls.some((url) => url.endsWith('/api/actionscat/actions')), 'enabling must resume action loading');
disposeActions();

console.log('ActionsCat disabled page checks passed.');
