import { strict as assert } from 'node:assert';
import { createInstanceAPI, securityAPI, actionsCatAPI } from '../src/api/client';
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
const catalogDraftOwners: string[] = [];
const catalogDraftResets: string[] = [];
globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = input instanceof Request ? input.url : String(input);
  (input instanceof Request ? input.signal : init?.signal)?.throwIfAborted();
  calls.push(url);
  logSources.push(
    new Headers(init?.headers).get('X-FrostAgent-Log-Source') ?? '',
  );
  catalogDraftOwners.push(new Headers(input instanceof Request ? input.headers : init?.headers)
    .get('X-FrostAgent-Catalog-Draft') ?? '');
  catalogDraftResets.push(new Headers(input instanceof Request ? input.headers : init?.headers)
    .get('X-FrostAgent-Catalog-Draft-Reset') ?? '');
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
await createInstanceAPI().listMCPServers();
assert.equal(
  new URL(calls[4]).pathname,
  '/instances/b1c2d3e4/frostagent.v1.MCPService/ListMCPServers',
  'MCP requests bypassed their owning instance',
);
await createInstanceAPI().listDialogues();
assert.equal(
  new URL(calls[5]).pathname,
  '/instances/b1c2d3e4/frostagent.v1.DialogueService/ListDialogues',
  'dialogue requests bypassed their owning instance',
);
await actionsCatAPI.listActions();
assert.equal(
  new URL(calls[6], 'http://localhost').pathname,
  '/instances/b1c2d3e4/api/actionscat/actions',
  'ActionsCat requests bypassed their owning instance',
);
instanceState.select(null);

// Capture the Request signal as well as requests which start after a switch.
const originalFetch = globalThis.fetch;
let requestStarted!: () => void;
const started = new Promise<void>((resolve) => { requestStarted = resolve; });
let pendingSignal: AbortSignal | undefined;
globalThis.fetch = (input: RequestInfo | URL, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
  pendingSignal = input instanceof Request ? input.signal : init?.signal ?? undefined;
  pendingSignal?.addEventListener('abort', () => reject(pendingSignal?.reason), { once: true });
  requestStarted();
});
const inFlightCatalog = createInstanceAPI({ globalModelCatalog: true }).getModelRouterState();
const rejectedCatalog = assert.rejects(inFlightCatalog);
await started;
instanceState.select(a);
assert.equal(pendingSignal?.aborted, true, 'switch did not abort the in-flight shared catalog Request');
await rejectedCatalog;
globalThis.fetch = originalFetch;
instanceState.select(null);
await assert.rejects(createInstanceAPI().listMCPServers(), /请先选择实例/);
await assert.rejects(createInstanceAPI().listDialogues(), /请先选择实例/);
await assert.rejects(actionsCatAPI.listActions(), /请先选择实例/);
assert.equal(calls.length, 7, 'zero-instance request reached the network');
instanceState.logSource = 'instance';
await assert.rejects(
  createInstanceAPI().listLogs(10, '', 0, ''),
  /请先选择实例/,
);
assert.equal(
  calls.length,
  7,
  'zero-instance log request silently used General',
);
const locked = await securityAPI.listLockedPrincipals();
assert.deepEqual(locked, { success: true });
assert.equal(new URL(calls[7], 'http://localhost').pathname, '/api/security/locked');
await securityAPI.unlockPrincipal('test', 'user');
assert.equal(new URL(calls[8], 'http://localhost').pathname, '/api/security/unlock');
assert.equal(calls.length, 9, 'security requests must use the global REST API');
instanceState.logSource = 'control-plane';
await createInstanceAPI().listLogs(10, '', 0, '');
assert.equal(
  new URL(calls[9], 'http://localhost').pathname,
  '/frostagent.v1.LogService/ListLogs',
  'explicit zero-instance General log request did not use the root endpoint',
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

instanceState.select(null);
await createInstanceAPI({ globalModelCatalog: true }).getModelRouterState();
assert.equal(new URL(calls.at(-1)!).pathname, '/frostagent.v1.ModelRouterService/GetState');
await assert.rejects(createInstanceAPI().getModelRouterState(), /请先选择实例/);
instanceState.select(a);
await createInstanceAPI().getModelRouterState();
assert.equal(new URL(calls.at(-1)!).pathname, '/instances/a1b2c3d4/frostagent.v1.ModelRouterService/GetState');
await createInstanceAPI({ globalModelCatalog: true }).publishModelRouter();
assert.equal(new URL(calls.at(-1)!).pathname, '/frostagent.v1.ModelRouterService/Publish');
await createInstanceAPI({ globalModelCatalog: true, catalogDraftOwner: 'synthetic-page-b' }).getModelRouterState();
assert.equal(catalogDraftOwners.at(-1), 'synthetic-page-b', 'shared catalog requests lost the editor owner');
await createInstanceAPI({ globalModelCatalog: true, catalogDraftOwner: 'synthetic-page-b' }).resetSharedCatalogDraft();
assert.equal(catalogDraftResets.at(-1), 'true', 'explicit shared draft reset lost its request marker');
console.log('PASS: shared providers use the root API and instance routes require a selected instance');

const oldCatalogPage = createInstanceAPI({ globalModelCatalog: true });
instanceState.select(b);
const catalogCallsBeforeSwitch = calls.length;
await assert.rejects(oldCatalogPage.publishModelRouter());
await assert.rejects(oldCatalogPage.setDraftEndpointSecret('synthetic-provider', 'synthetic-key'));
assert.equal(calls.length, catalogCallsBeforeSwitch, 'stale model editor mutated the shared catalog after switching instances');
instanceState.select(null);
