import assert from 'node:assert/strict';
import { mountActionsCatPage } from '../src/pages/actionscat';
import { mountBackendSettingsPage } from '../src/pages/backend-settings';
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
  private renderedToggles = '';
  private toggles: MockElement[] = [];

  addEventListener(name: string, listener: () => unknown) { this.listeners.set(name, listener); }
  trigger(name: string) { return this.listeners.get(name)?.(); }
  querySelector(selector: string): MockElement {
    if (!this.children.has(selector)) this.children.set(selector, new MockElement());
    return this.children.get(selector)!;
  }
  querySelectorAll(selector: string): MockElement[] {
    if (selector !== '[data-action="toggle-setting"]') return [];
    if (this.renderedToggles !== this.innerHTML) {
      this.renderedToggles = this.innerHTML;
      this.toggles = [...this.innerHTML.matchAll(/<input[^>]*data-action="toggle-setting"[^>]*>/g)].map(([html]) => {
        const input = new MockElement();
        input.dataset.key = /data-key="([^"]+)"/.exec(html)![1];
        input.checked = html.includes('checked');
        return input;
      });
    }
    return this.toggles;
  }
}

Object.assign(globalThis, {
  document: { querySelectorAll: () => [] },
  window: Object.assign(new EventTarget(), { location: { origin: 'http://localhost' } }),
});
toast.success = () => {};
instanceState.select({ id: 'test-instance', name: 'Test', enabled: true, created_at: '' });
const calls: string[] = [];
const writes: { key: string; value: string }[] = [];
let disabled = true;
let settingValue = '';
globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = input instanceof Request ? input.url : String(input);
  calls.push(url);
  let body: unknown = [];
  if (url.endsWith('/status')) {
    body = { disabled, configured: true, healthy: !disabled, authenticated: !disabled, endpoint: 'http://test.invalid' };
  } else if (url.endsWith('/ListEnvVars')) {
    body = { envVars: [{ key: 'ACTIONSCAT_ENABLED', value: settingValue, isSecret: false }] };
  } else if (url.endsWith('/UpdateEnvVar')) {
    const request = input instanceof Request ? input : new Request(url, init);
    const write = await request.json();
    writes.push(write);
    settingValue = write.value;
    body = { success: true };
  } else if (url.endsWith('/GetSessions')) {
    body = { sessions: [] };
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

const settingsRoot = new MockElement();
const disposeSettings = mountBackendSettingsPage(settingsRoot as unknown as HTMLElement);
await settle();
const table = settingsRoot.querySelector('#env-table-body');
// An old instance with no stored value must display the enabled default.
let toggle = table.querySelectorAll('[data-action="toggle-setting"]')[0];
assert.ok(toggle, 'ActionsCat setting must render a boolean control');
assert.equal(toggle.checked, true);
toggle.checked = false;
toggle.trigger('change');
await settle();
assert.deepEqual(writes.map(({ key, value }) => ({ key, value })), [{ key: 'ACTIONSCAT_ENABLED', value: 'false' }]);
toggle = table.querySelectorAll('[data-action="toggle-setting"]')[0];
assert.equal(toggle.checked, false, 'saved disabled state must remain visible');
disposeSettings();
console.log('ActionsCat disabled page and settings interaction checks passed.');
