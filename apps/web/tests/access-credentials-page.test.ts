import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { getControlToken, controlPlaneFetch, directChatWebSocket } from '../src/api/control-auth';
import { toast } from '../src/components/toast';
import { instanceState } from '../src/instance-state';
import { mountAccessCredentialsPage } from '../src/pages/access-credentials';
import { mountSettingsPage } from '../src/pages/settings';

const { JSDOM } = createRequire(resolve('package.json'))('jsdom');
const dom = new JSDOM('<body><main id="root"></main></body>', { url: 'https://panel.example/#/settings/credentials' });
const originals = {
  window: globalThis.window, document: globalThis.document, localStorage: globalThis.localStorage,
  fetch: globalThis.fetch, WebSocket: globalThis.WebSocket, Event: globalThis.Event,
};
const originalToast = toast.success;
Object.assign(globalThis, { window: dom.window, document: dom.window.document, localStorage: dom.window.localStorage, Event: dom.window.Event });
dom.window.matchMedia = () => ({ matches: false, addEventListener() {} });
const root = document.querySelector<HTMLElement>('#root')!;
const messages: string[] = [];
toast.success = (message) => { messages.push(message); };
const input = () => root.querySelector<HTMLInputElement>('#control-token-input')!;
const button = (id: string) => root.querySelector<HTMLButtonElement>(`#${id}`)!;
let cleanup: (() => void) | undefined;
try {
  instanceState.select(null);
  globalThis.fetch = async () => { throw new Error('credentials page must not require an authenticated API'); };
  cleanup = mountSettingsPage(root);
  const entry = root.querySelector<HTMLAnchorElement>('a[href="#/settings/credentials"]');
  assert.ok(entry, 'Settings must have a separate credentials entry without an instance');
  assert.equal(entry.querySelector('h2')?.textContent, '访问凭据');
  assert.ok(root.querySelector('a[href="#/settings/frontend"]'), 'appearance entry must stay separate');
  cleanup();

  const existing = 'synthetic-legacy-credential';
  localStorage.setItem('frostagent_control_token', existing);
  cleanup = mountAccessCredentialsPage(root);
  assert.equal(root.querySelector('h1')?.textContent, '访问凭据');
  assert.ok(root.querySelector('a[href="#/settings"]'), 'new page must link back to Settings');
  assert.equal(input().value, existing, 'existing browser credentials need no migration');
  assert.equal(input().type, 'password');
  assert.match(root.querySelector('#control-token-status')!.textContent!, /已配置/);
  assert.equal(root.querySelector('[data-mode]'), null, 'credentials must not include appearance controls');
  assert.ok(!root.innerHTML.includes(existing), 'credential must not be interpolated into HTML');
  button('toggle-token-visibility').click();
  assert.equal(input().type, 'text');
  assert.equal(button('toggle-token-visibility').getAttribute('aria-pressed'), 'true');
  button('toggle-token-visibility').click();
  assert.equal(input().type, 'password');

  const edited = 'synthetic-"<img>credential';
  input().value = ` ${edited} `;
  assert.equal(getControlToken(), existing, 'editing is local until Save');
  button('save-token-btn').click();
  assert.equal(getControlToken(), edited, 'Save must keep the original trimming/storage contract');
  assert.equal(input().value, edited);
  assert.equal(input().type, 'password', 'Save resets visibility');
  assert.equal(root.querySelector('img'), null);
  assert.ok(!root.innerHTML.includes(edited));
  assert.deepEqual(messages, ['访问凭据已保存']);

  let authorization: string | null = null;
  globalThis.fetch = async (_request, init) => {
    authorization = new Headers(init?.headers).get('Authorization');
    return Response.json({ success: true });
  };
  await controlPlaneFetch('/api/instances');
  assert.equal(authorization, `Bearer ${edited}`, 'the new page must feed the existing API authentication');
  const sockets: { url: string; protocols: string[] }[] = [];
  Object.assign(globalThis, { WebSocket: class {
    constructor(url: string, protocols: string[]) { sockets.push({ url, protocols }); }
  } });
  directChatWebSocket('wss://panel.example/instances/synthetic/ws/onebot?mock=true');
  assert.equal(sockets[0].protocols[1], `frostagent.control-token.${Buffer.from(edited).toString('base64url')}`);
  assert.ok(!sockets[0].url.includes(edited));

  input().value = 'synthetic-unsaved-draft';
  const staleInput = input();
  const staleSave = button('save-token-btn');
  cleanup();
  assert.equal(staleInput.value, '', 'leaving clears the detached credential input');
  staleInput.value = 'synthetic-stale-write';
  staleSave.click();
  assert.equal(getControlToken(), edited, 'an unmounted page must not save credentials');
  cleanup = mountAccessCredentialsPage(root);
  assert.equal(input().value, edited, 'remount retains only the saved value');
  button('clear-token-btn').click();
  assert.equal(getControlToken(), '');
  assert.equal(localStorage.getItem('frostagent_control_token'), null);
  assert.equal(input().value, '');
  assert.equal(root.querySelector('#clear-token-btn'), null);
  assert.match(root.querySelector('#control-token-status')!.textContent!, /未设置/);
  assert.equal(messages.at(-1), '访问凭据已清除');
  input().value = existing;
  button('save-token-btn').click();
  input().value = '   ';
  button('save-token-btn').click();
  assert.equal(getControlToken(), '', 'saving a blank token must still clear it');
  cleanup();

  localStorage.setItem('frostagent_control_token', existing);
  const { mountFrontendSettingsPage } = await import('../src/pages/frontend-settings');
  cleanup = mountFrontendSettingsPage(root);
  assert.equal(root.querySelector('h1')?.textContent, '网页端外观设置');
  assert.equal(root.querySelectorAll('[data-mode]').length, 3);
  assert.equal(root.querySelector('#control-token-input'), null, 'appearance page must no longer contain credentials');
  for (const mode of ['dark', 'light', 'system']) {
    root.querySelector<HTMLButtonElement>(`[data-mode="${mode}"]`)!.click();
    assert.equal(localStorage.getItem('user-theme'), mode);
    assert.equal(getControlToken(), existing, 'theme changes must not alter credentials');
  }

  const main = readFileSync('src/main.ts', 'utf8');
  assert.match(main, /router\.register\('\/settings\/credentials', '访问凭据', mountAccessCredentialsPage\)/);
  const guarded = main.match(/const guardedPaths = \[([\s\S]*?)\];/)?.[1];
  assert.ok(guarded);
  assert.ok(!guarded.includes('/settings/credentials'), 'credentials route must work before choosing an instance');
  const mcp = readFileSync('src/pages/mcp.ts', 'utf8');
  assert.match(mcp, /href="#\/settings\/credentials"/);
  assert.doesNotMatch(mcp, /#\/settings\/frontend/);
  const appearance = readFileSync('src/pages/frontend-settings.ts', 'utf8');
  assert.doesNotMatch(appearance, /getControlToken|setControlToken|访问凭据/);
} finally {
  cleanup?.();
  toast.success = originalToast;
  Object.assign(globalThis, originals);
  dom.window.close();
}
console.log('PASS: separate credentials page, existing storage, save/clear, auth integration and appearance isolation');
