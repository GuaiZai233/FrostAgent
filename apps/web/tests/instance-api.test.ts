import { strict as assert } from 'node:assert';
import { api, createInstanceAPI } from '../src/api/client';
import { instanceState } from '../src/instance-state';
import { RequestGeneration } from '../src/utils/request-generation';
import { instanceWebSocketURL } from '../src/utils/websocket-url';
const events = new EventTarget();
Object.assign(globalThis, {
  window: Object.assign(events, { location: { origin: 'http://localhost' } }),
  document: { querySelectorAll: () => [] },
});
const calls: string[] = [];
const logSources: string[] = [];
globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = input instanceof Request ? input.url : String(input);
  init?.signal?.throwIfAborted();
  calls.push(url);
  logSources.push(
    new Headers(init?.headers).get('X-FrostAgent-Log-Source') ?? '',
  );
  return new Response(JSON.stringify({ success: true }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
};
const a = { id: 'a1b2c3d4', name: 'a', enabled: false, created_at: '' };
const b = { id: 'b1c2d3e4', name: 'b', enabled: false, created_at: '' };
assert.equal(
  instanceWebSocketURL(':1234', a.id, 'astrbot', 'http://localhost:8080'),
  'ws://localhost:1234/instances/a1b2c3d4/ws/astrbot',
  'adapter URL used the management HTTP port instead of WS_LISTEN_ADDR',
);
assert.equal(
  instanceWebSocketURL(
    '0.0.0.0:1234',
    a.id,
    'onebot',
    'http://dashboard.example:8080',
  ),
  'ws://dashboard.example:1234/instances/a1b2c3d4/ws/onebot',
  'wildcard listener host was exposed instead of the dashboard host',
);
instanceState.select(a);
const oldPage = createInstanceAPI();
// Simulates deferred FileReader/arrayBuffer continuations and the next item of
// a batch that resumes after the user switches instance.
const delayedUpload = async () => {
  await Promise.resolve();
  await oldPage.uploadSticker(new Uint8Array([1, 2, 3]), 'synthetic.png');
};
const pending = delayedUpload();
instanceState.select(b);
await assert.rejects(pending);
await assert.rejects(oldPage.importMemories('{"entries":[]}', false));
await assert.rejects(oldPage.uploadSticker(new Uint8Array([1]), 'second.png'));
assert.equal(calls.length, 0, 'stale page transmitted data after a switch');
await createInstanceAPI().updateEnvVar({
  key: 'BOT_NAME',
  value: 'b',
  isSecret: false,
});
assert.equal(calls.length, 1);
assert.ok(calls[0].includes('/instances/b1c2d3e4/'));
assert.equal(logSources[0], 'instance');
instanceState.logSource = 'control-plane';
await createInstanceAPI().listLogs(10, '', 0, '');
assert.equal(logSources[1], 'control-plane');
assert.equal(
  new URL(calls[1]).pathname,
  '/frostagent.v1.LogService/ListLogs',
  'Control Plane logs were routed through the selected instance',
);
await createInstanceAPI().clearLogs();
assert.equal(
  new URL(calls[2]).pathname,
  '/frostagent.v1.LogService/ClearLogs',
  'Control Plane log mutations were routed through the selected instance',
);
instanceState.logSource = 'instance';
await createInstanceAPI().listLogs(10, '', 0, '');
assert.equal(
  new URL(calls[3]).pathname,
  '/instances/b1c2d3e4/frostagent.v1.LogService/ListLogs',
  'instance logs bypassed their instance route',
);
await api.listMCPServers();
assert.equal(
  new URL(calls[4]).pathname,
  '/frostagent.v1.MCPService/ListMCPServers',
  'Control Plane MCP requests were routed through the selected instance',
);
instanceState.select(null);
await api.listMCPServers();
assert.equal(
  new URL(calls[5]).pathname,
  '/frostagent.v1.MCPService/ListMCPServers',
  'Control Plane MCP requests required a selected instance',
);

const requestGeneration = new RequestGeneration();
let visibleLogSource = '';
let releaseOldLogs!: () => void;
const oldLogs = new Promise<void>((resolve) => {
  releaseOldLogs = resolve;
});
const loadLogSource = async (source: string, response: Promise<void>) => {
  const generation = requestGeneration.next();
  await response;
  if (requestGeneration.isCurrent(generation)) visibleLogSource = source;
};
const oldLogRequest = loadLogSource('instance', oldLogs);
await loadLogSource('control-plane', Promise.resolve());
releaseOldLogs();
await oldLogRequest;
assert.equal(
  visibleLogSource,
  'control-plane',
  'late instance response replaced the selected Control Plane source',
);
console.log(
  'PASS: stale instance continuations and log responses are rejected',
);
