import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { setImmediate } from 'node:timers/promises';
import { mountChatPage } from '../src/pages/chat';
import { instanceState } from '../src/instance-state';
import { router } from '../src/router';

const { JSDOM } = createRequire(resolve('package.json'))('jsdom');
const dom = new JSDOM('<body><main></main></body>', { url: 'http://panel.example/#/overview' });
const originals = {
  window: globalThis.window, document: globalThis.document, Event: globalThis.Event,
  WebSocket: globalThis.WebSocket, fetch: globalThis.fetch,
  localStorage: globalThis.localStorage,
};
Object.assign(globalThis, { window: dom.window, document: dom.window.document, Event: dom.window.Event,
  localStorage: dom.window.localStorage });
localStorage.setItem('frostagent_control_token', 'synthetic-navigation-token');

class Socket {
  static OPEN = 1;
  static all: Socket[] = [];
  readyState = 0;
  sent: Record<string, unknown>[] = [];
  closes = 0;
  onopen: (() => void) | null = null;
  onclose: ((event: { code: number; reason: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  constructor(readonly url: string, readonly protocols: string[]) {
    assert.deepEqual(protocols, ['frostagent.direct-chat',
      `frostagent.control-token.${Buffer.from('synthetic-navigation-token').toString('base64url')}`]);
    Socket.all.push(this);
  }
  open() { this.readyState = 1; this.onopen?.(); }
  send(data: string) { assert.equal(this.readyState, 1); this.sent.push(JSON.parse(data)); }
  receive(frame: unknown) { this.onmessage?.({ data: JSON.stringify(frame) }); }
  close() { this.closes++; this.readyState = 3; this.onclose?.({ code: 1000, reason: '' }); }
}
Object.assign(globalThis, { WebSocket: Socket });
let pendingOverview: (() => void) | undefined;
let deferOverview = false;
globalThis.fetch = async (input) => {
  const path = new URL(input instanceof Request ? input.url : String(input), dom.window.location.origin).pathname;
  assert.ok(path.endsWith('/GetOverview'), path);
  if (deferOverview) await new Promise<void>((resolve) => { pendingOverview = resolve; });
  return Response.json({ botName: 'Synthetic bot' });
};
const root = document.querySelector<HTMLElement>('main')!;
const flush = async () => { for (let i = 0; i < 8; i++) await setImmediate(); };
const navigate = (path: string) => { dom.window.history.replaceState(null, '', `#${path}`); router.refresh(); };
const click = (id: string) => root.querySelector<HTMLButtonElement>(`#${id}`)!.click();
function field(id: string, value: string, event = 'input') {
  const input = root.querySelector<HTMLInputElement>(`#${id}`)!;
  input.value = value;
  input.dispatchEvent(new dom.window.Event(event, { bubbles: true }));
}
const socket = () => Socket.all.at(-1)!;
const instance = { id: 'fixture-chat-a', name: 'A', enabled: true, created_at: '' };
router.register('/overview', 'Overview', (container) => { container.textContent = 'Overview'; });
router.register('/chat', 'Chat', mountChatPage);
const refresh = () => router.refresh();
dom.window.addEventListener('instance-selected', refresh);
router.init(root);

try {
  for (const adapter of ['onebot', 'astrbot']) {
    instanceState.select(instance);
    navigate('/chat');
    await flush();
    if (adapter === 'astrbot') field('chat-adapter-select', adapter, 'change');
    const live = socket();
    live.open();
    assert.ok(live.url.includes(`/instances/${instance.id}/ws/${adapter}?mock=true`));
    field('chat-type-select', 'group', 'change');
    field('chat-astrbot-platform-select', 'telegram', 'change');
    field('chat-uid-input', '90001');
    field('chat-nickname-input', 'Synthetic sender');
    field('chat-group-id-input', '90002');
    field('chat-group-name-input', 'Synthetic group');
    field('chat-card-input', 'Synthetic card');
    const wake = root.querySelector<HTMLInputElement>('#chat-is-wake-checkbox')!;
    wake.checked = false;
    wake.dispatchEvent(new dom.window.Event('change'));
    field('chat-input-textarea', `first-${adapter}`);
    click('btn-send-message');
    field('chat-input-textarea', 'unsent draft');
    const requests = Socket.all.length;
    const reply = adapter === 'onebot'
      ? { action: 'send_group_msg', params: { message: 'background final' }, echo: 'delivery-final' }
      : { action: 'send_message', content: 'background final', echo: 'reply-final' };
    navigate('/overview');
    assert.equal(live.closes, 0, 'route cleanup must keep the backend mock session alive');
    if (adapter === 'onebot') {
      live.receive({ action: 'get_group_info', echo: 'lookup' });
      const ack = live.sent.at(-1)!;
      assert.equal(ack.echo, 'lookup');
      assert.deepEqual(ack.data, { message_id: (ack.data as { message_id: number }).message_id, group_id: 90002, group_name: 'Synthetic group' });
    } else {
      live.receive({ action: 'send_message', content: 'background tool output', is_intermediate: true });
    }
    live.receive(reply);
    assert.equal(root.textContent, 'Overview', 'background replies must stay off the active route');
    navigate('/chat');
    await flush();
    assert.equal(Socket.all.length, requests, 'returning must reuse the same socket without replaying input');
    assert.equal(root.querySelector<HTMLInputElement>('#chat-input-textarea')!.value, 'unsent draft');
    for (const [id, value] of [
      ['chat-adapter-select', adapter], ['chat-type-select', 'group'], ['chat-astrbot-platform-select', 'telegram'],
      ['chat-uid-input', '90001'], ['chat-nickname-input', 'Synthetic sender'],
      ['chat-group-id-input', '90002'], ['chat-group-name-input', 'Synthetic group'], ['chat-card-input', 'Synthetic card'],
    ]) assert.equal(root.querySelector<HTMLInputElement>(`#${id}`)!.value, value);
    assert.equal(root.querySelector<HTMLInputElement>('#chat-is-wake-checkbox')!.checked, false);
    assert.equal(root.textContent!.split('background final').length - 1, 1);
    if (adapter === 'astrbot') assert.ok(root.textContent!.includes('background tool output'));
    else assert.equal(live.sent.at(-1)!.echo, 'delivery-final', 'delivery ACK must be sent while detached');
    click('btn-clear-chat');
    assert.equal(live.closes, 0, 'clear screen must retain model context');
    assert.ok(!root.textContent!.includes('background final'));
    field('chat-input-textarea', 'followup');
    click('btn-send-message');
    const events = live.sent.filter((frame) => frame.post_type === 'message' || frame.type === 'event');
    assert.equal(events.length, 2, 'route return must not duplicate sends');
    if (adapter === 'astrbot') {
      assert.equal(events[1].session_id, 'telegram:group:90002');
      assert.equal(events[1].user_id, '90001');
      assert.equal(events[1].is_wake, false);
    } else {
      assert.equal(events[1].group_id, 90002);
      assert.equal(events[1].user_id, 90001);
    }

    const staleMessage = live.onmessage!;
    click('btn-disconnect');
    assert.equal(live.closes, 1);
    navigate('/overview');
    navigate('/chat');
    assert.equal(Socket.all.length, requests, 'manual disconnect must survive navigation');
    click('btn-reconnect');
    const reconnected = socket();
    reconnected.open();
    assert.ok(root.textContent!.includes('followup'), 'reconnect preserves screen records');
    staleMessage({ data: JSON.stringify(reply) });
    assert.ok(!root.textContent!.includes('background final'), 'old callbacks must not write into a new connection');
    click('btn-reset-session');
    assert.equal(reconnected.closes, 1);
    assert.ok(!root.textContent!.includes('followup'), 'reset clears screen and reconnects');
    const reset = socket();
    reset.open();

    reset.receive(reply);
    const staleProtocol = reset.onmessage!;
    const otherAdapter = adapter === 'onebot' ? 'astrbot' : 'onebot';
    field('chat-adapter-select', otherAdapter, 'change');
    const switched = socket();
    switched.open();
    assert.equal(reset.closes, 1, 'switching protocol must close its old mock namespace');
    assert.ok(switched.url.includes(`/ws/${otherAdapter}?mock=true`));
    staleProtocol({ data: JSON.stringify(reply) });
    assert.ok(!root.textContent!.includes('background final'), 'protocol switches must clear old messages');

    navigate('/overview');
    const staleReset = switched.onmessage!;
    instanceState.select({ ...instance, id: 'fixture-chat-b' });
    assert.equal(switched.closes, 1, 'changing instances off-route must dispose the old connection');
    navigate('/chat');
    await flush();
    staleReset({ data: JSON.stringify(reply) });
    assert.ok(socket().url.includes('/instances/fixture-chat-b/'));
    assert.match(root.querySelector<HTMLInputElement>('#chat-uid-input')!.value, /^[1-9]\d{4}$/);
    assert.ok(!root.textContent!.includes(`first-${adapter}`));
    assert.ok(!root.textContent!.includes('background final'));
    instanceState.select(null);
    navigate('/overview');
  }

  // Navigation while overview is still loading must initialize only one socket.
  deferOverview = true;
  instanceState.select(instance);
  navigate('/chat');
  await flush();
  const before = Socket.all.length;
  navigate('/overview');
  pendingOverview!();
  await flush();
  assert.equal(Socket.all.length, before + 1);
  socket().open();
  navigate('/chat');
  await flush();
  assert.equal(Socket.all.length, before + 1);

  // An explicit disconnect made before overview returns must stay disconnected.
  instanceState.select(instance);
  await flush();
  click('btn-disconnect');
  const disconnectedCount = Socket.all.length;
  pendingOverview!();
  await flush();
  assert.equal(Socket.all.length, disconnectedCount);

  // A late overview for a disposed instance must never open a socket for its replacement.
  instanceState.select(instance);
  await flush();
  const lateOverview = pendingOverview!;
  navigate('/overview');
  instanceState.select({ ...instance, id: 'fixture-chat-c' });
  lateOverview();
  await flush();
  assert.equal(Socket.all.length, disconnectedCount);
} finally {
  instanceState.select(null);
  navigate('/overview');
  dom.window.removeEventListener('instance-selected', refresh);
  dom.window.close();
  Object.assign(globalThis, originals);
}
console.log('PASS: direct-chat route continuity, background frames, controls and instance isolation for both adapters');
