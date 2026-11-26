import { createTestDOM, emit } from './dom-fixture';
import assert from 'node:assert/strict';
import { mountActionsCatPage } from '../src/pages/actionscat';
import { instanceState } from '../src/instance-state';
import { toast } from '../src/components/toast';

const dom = createTestDOM();
try {
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

  const actionsRoot = dom.root();
  const disposeActions = mountActionsCatPage(actionsRoot as unknown as HTMLElement);
  await settle();
  assert.equal(actionsRoot.querySelector('#actionscat-tools-card').hidden, true);
  assert.equal(actionsRoot.querySelector('#actionscat-dispatch-btn').disabled, true);
  assert.equal(actionsRoot.querySelector('#actionscat-create-action-btn').disabled, true);
  assert.match(actionsRoot.querySelector('#actionscat-status-container').innerHTML, /ActionsCat已被FrostAgent管理员禁用。/);
  assert.equal(calls.filter((url) => url.includes('/api/actionscat')).length, 1, 'disabled page must only query status');
  disabled = false;
  emit(actionsRoot.querySelector('#actionscat-refresh-btn'), 'click');
  await settle();
  assert.equal(actionsRoot.querySelector('#actionscat-tools-card').hidden, false);
  assert.equal(actionsRoot.querySelector('#actionscat-dispatch-btn').disabled, false);
  assert.equal(actionsRoot.querySelector('#actionscat-create-action-btn').disabled, false);
  assert.ok(calls.some((url) => url.endsWith('/api/actionscat/actions')), 'enabling must resume action loading');
  disposeActions();

  console.log('ActionsCat disabled page checks passed.');
} finally {
  dom.cleanup();
}
