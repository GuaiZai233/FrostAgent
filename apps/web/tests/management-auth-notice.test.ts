import assert from 'node:assert/strict';
import { createTestDOM, query } from './dom-fixture';
import { controlPlaneFetch, setControlToken } from '../src/api/control-auth';
import { createInstanceAPI } from '../src/api/client';
import { instanceState } from '../src/instance-state';
import { mountManagementAuthNotice } from '../src/components/management-auth-notice';

const fixture = createTestDOM('https://panel.example/#/settings/credentials');
const root = fixture.root();
const cleanup = mountManagementAuthNotice(root);
try {
  assert.equal(root.hidden, false);
  assert.match(root.textContent!, /本机浏览器/);
  assert.match(root.textContent!, /FROSTAGENT_BOOTSTRAP_CONTROL_TOKEN/);
  assert.equal(query<HTMLAnchorElement>(root, 'a').hash, '#/settings/credentials');
  setControlToken('synthetic-control-token');
  assert.equal(root.hidden, true);
  globalThis.fetch = async () => new Response('unauthorized', { status: 401 });
  await assert.rejects(controlPlaneFetch('/api/version'), /访问凭据/);
  assert.equal(root.hidden, false);
  assert.match(root.textContent!, /未通过验证/);
  assert.ok(!root.innerHTML.includes('synthetic-control-token'));
  setControlToken('synthetic-next-token');
  instanceState.select({ id: 'synthetic-instance', name: 'Test', enabled: false, created_at: '' });
  await assert.rejects(createInstanceAPI().getOverview());
  assert.equal(root.hidden, false, 'RPC authentication failures must show the same guidance');
  setControlToken('');
  assert.match(root.textContent!, /请先设置/);
  cleanup();
  setControlToken('synthetic-detached');
  assert.equal(root.hidden, false, 'unmounted notice must not react');
} finally {
  cleanup();
  instanceState.select(null);
  fixture.cleanup();
}
console.log('PASS: missing and rejected management credentials expose initialization guidance');
