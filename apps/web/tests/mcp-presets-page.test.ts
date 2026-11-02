import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers/promises';
import { mountMCPPage } from '../src/pages/mcp';
import { instanceState } from '../src/instance-state';
import { toast } from '../src/components/toast';

const decodeHTML = (value: string) => value.replace(
  /&(?:amp|quot|lt|gt|#39|#10);/g,
  (entity) => ({ '&amp;': '&', '&quot;': '"', '&lt;': '<', '&gt;': '>', '&#39;': "'", '&#10;': '\n' })[entity]!,
);

// A small DOM fixture exercising the real page and dialog event handlers.
class MockElement {
  private html = '';
  private children: MockElement[] = [];
  private attributes = new Map<string, string>();
  private listeners = new Map<string, ((event: unknown) => unknown)[]>();
  private isChecked = false;
  private parent?: MockElement;
  value = '';
  disabled = false;
  hidden = false;
  removed = false;
  textContent = '';
  className = '';
  style: Record<string, string> = {};
  dataset: Record<string, string> = {};
  constructor(readonly tag = 'div') {}
  get type() { return this.getAttribute('type') ?? ''; }
  get checked() { return this.isChecked; }
  set checked(value: boolean) {
    if (value && this.type === 'radio') {
      for (const sibling of this.parent?.children ?? []) {
        if (sibling.type === 'radio' && sibling.getAttribute('name') === this.getAttribute('name')) sibling.isChecked = false;
      }
    }
    this.isChecked = value;
  }
  get innerHTML() { return this.html; }
  set innerHTML(html: string) {
    this.html = html;
    this.children = [];
    for (const match of html.matchAll(/<([a-z][\w-]*)\b([^>]*)>/g)) {
      const child = new MockElement(match[1]);
      for (const attr of match[2].matchAll(/([\w-]+)(?:=(?:"([^"]*)"|'([^']*)'))?/g)) {
        child.setAttribute(attr[1], decodeHTML(attr[2] ?? attr[3] ?? ''));
      }
      child.value = child.getAttribute('value') ?? '';
      child.isChecked = child.attributes.has('checked');
      if (child.tag === 'textarea') {
        child.value = decodeHTML(html.slice(match.index! + match[0].length).split('</textarea>')[0]);
      }
      this.appendChild(child);
    }
  }
  classList = {
    contains: (name: string) => this.className.split(/\s+/).includes(name),
    add: (...names: string[]) => { this.className = [...new Set([...this.className.split(/\s+/).filter(Boolean), ...names])].join(' '); },
    remove: (...names: string[]) => { this.className = this.className.split(/\s+/).filter((name) => !names.includes(name)).join(' '); },
    toggle: (name: string, force?: boolean) => {
      const add = force ?? !this.classList.contains(name);
      if (add) this.classList.add(name); else this.classList.remove(name);
      return add;
    },
  };
  setAttribute(name: string, value: string) {
    this.attributes.set(name, value);
    if (name === 'class') this.className = value;
    if (name === 'disabled') this.disabled = true;
    if (name === 'hidden') this.hidden = true;
    if (name.startsWith('data-')) this.dataset[name.slice(5).replace(/-([a-z])/g, (_, letter: string) => letter.toUpperCase())] = value;
  }
  getAttribute(name: string) { return name === 'class' ? this.className : this.attributes.get(name) ?? null; }
  removeAttribute(name: string) {
    this.attributes.delete(name);
    if (name === 'disabled') this.disabled = false;
    if (name === 'hidden') this.hidden = false;
  }
  appendChild(child: MockElement) { child.parent = this; this.children.push(child); return child; }
  private all(): MockElement[] { return this.children.flatMap((child) => [child, ...child.all()]); }
  private matches(selector: string) {
    // The page uses only simple selectors and a tab-container descendant selector.
    const simple = selector.trim().split(/\s+/).at(-1)!;
    const tag = simple.match(/^[a-z][\w-]*/)?.[0];
    if (tag && tag !== this.tag) return false;
    const id = simple.match(/#([\w-]+)/)?.[1];
    if (id && this.getAttribute('id') !== id) return false;
    for (const [, name] of simple.matchAll(/\.([\w-]+)/g)) if (!this.classList.contains(name)) return false;
    for (const [, name, value] of simple.matchAll(/\[([\w-]+)(?:="([^"]*)")?\]/g)) {
      if (this.getAttribute(name) === null || (value !== undefined && this.getAttribute(name) !== value)) return false;
    }
    return !simple.includes(':checked') || this.checked;
  }
  querySelectorAll(selector: string): MockElement[] {
    return this.all().filter((child) => selector.split(',').some((part) => child.matches(part)));
  }
  querySelector(selector: string) { return this.querySelectorAll(selector)[0] ?? null; }
  addEventListener(name: string, listener: (event: unknown) => unknown) {
    this.listeners.set(name, [...this.listeners.get(name) ?? [], listener]);
  }
  async fire(name: string) {
    for (const listener of this.listeners.get(name) ?? []) {
      await listener({ target: this, currentTarget: this, preventDefault() {} });
    }
  }
  async click() { if (!this.disabled) await this.fire('click'); }
  focus() {}
  showModal() {}
  close() {}
  remove() { this.removed = true; }
}

const dialogs: MockElement[] = [];
Object.assign(globalThis, {
  document: {
    querySelectorAll: () => [],
    createElement: (tag: string) => new MockElement(tag),
    body: { appendChild: (dialog: MockElement) => dialogs.push(dialog) },
  },
  window: Object.assign(new EventTarget(), { location: { origin: 'http://localhost' } }),
});
instanceState.select({ id: 'preset-fixture', name: 'Fixture', enabled: true, created_at: '' });
const notices: string[] = [];
toast.success = toast.error = toast.warning = (message) => { notices.push(message); };

const fixtureServer = (id: string) => ({
  id, name: `Existing ${id}`, enabled: true, transportType: 'streamable_http',
  url: 'https://existing.invalid/mcp', headers: { Authorization: 'Bearer synthetic-existing-token' },
  status: 'connected', tools: [], toolsCount: 0, enabledToolsCount: 0,
});
const servers = [fixtureServer('anysearch'), fixtureServer('anysearch_2')];
const calls: string[] = [];
const saved: { method: string; body: Record<string, unknown> }[] = [];
globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  const request = input instanceof Request ? input : new Request(String(input), init);
  const method = new URL(request.url).pathname.split('/').at(-1)!;
  calls.push(method);
  assert.ok(request.url.includes('/instances/preset-fixture/frostagent.v1.MCPService/'));
  if (method === 'ListMCPServers') return Response.json({ servers });
  assert.ok(['AddMCPServer', 'UpdateMCPServer'].includes(method), `unexpected MCP call: ${method}`);
  saved.push({ method, body: await request.json() as Record<string, unknown> });
  return Response.json({ success: true });
};
const settle = async () => { for (let i = 0; i < 5; i++) await setImmediate(); };
const root = new MockElement();
const dispose = mountMCPPage(root as unknown as HTMLElement);
const el = (element: MockElement, selector: string) => {
  const found = element.querySelector(selector);
  assert.ok(found, `missing element ${selector}`);
  return found;
};
const type = async (element: MockElement, value: string) => { element.value = value; await element.fire('input'); };
const json = (dialog: MockElement) => JSON.parse(el(dialog, '#form-json').value) as Record<string, unknown>;
const openAdd = async () => {
  await el(root, '#mcp-add-server-btn').click();
  return dialogs.at(-1)!;
};

await settle();
assert.deepEqual(calls, ['ListMCPServers']);
const dialog = await openAdd();
assert.deepEqual(dialog.querySelectorAll('[data-mcp-preset]').map((button) => button.dataset.mcpPreset), ['anysearch', 'github']);
const fields = el(dialog, '#mcp-preset-fields');
assert.ok(fields.hidden || fields.classList.contains('hidden'));
const token = el(dialog, '#mcp-preset-token');
assert.equal(token.type, 'password');
assert.equal(token.getAttribute('autocomplete'), 'off');
assert.equal(token.value, '');

// Selecting a preset must not overwrite a manually entered draft or submit it.
await el(dialog, '[data-tab="json"]').click();
await type(el(dialog, '#form-json'), JSON.stringify({
  id: 'manual-before-preset', name: 'Manual draft', transportType: 'stdio', command: 'node',
  args: ['server.js'], env: { OLD_SECRET: 'synthetic-old-secret' },
  headers: { 'X-Old-Header': 'synthetic-old-header' },
}));
const manualDraft = json(dialog);
await el(dialog, '[data-mcp-preset="anysearch"]').click();
assert.equal(el(dialog, '[data-mcp-preset="anysearch"]').getAttribute('aria-pressed'), 'true');
assert.ok(!fields.hidden && !fields.classList.contains('hidden'));
assert.deepEqual(json(dialog), manualDraft);
assert.equal(el(dialog, '#mcp-preset-description').textContent, '联网搜索与网页内容提取，填写自己的 AnySearch API Key。 https://api.anysearch.com/mcp');

for (const invalid of ['', '   ', 'Bearer', 'synthetic-invalid\r\nX-Injected: yes', 'synthetic invalid']) {
  await type(token, invalid);
  await el(dialog, '#mcp-preset-apply').click();
  assert.deepEqual(json(dialog), manualDraft, 'invalid tokens must not replace a draft');
}
assert.ok(!notices.some((notice) => notice.includes('synthetic-invalid') || notice.includes('synthetic invalid')));
await type(token, '  bEaReR synthetic-anysearch-token  ');
await el(dialog, '#mcp-preset-apply').click();
assert.deepEqual(json(dialog), {
  id: 'anysearch_3', name: 'AnySearch', enabled: true, transportType: 'streamable_http',
  url: 'https://api.anysearch.com/mcp',
  headers: { Authorization: 'Bearer synthetic-anysearch-token', 'X-Anysearch-Client': 'mcp/1.0.0' },
});
assert.equal(token.value, '', 'applied token must leave the preset input');
assert.equal(el(dialog, '#form-url').value, 'https://api.anysearch.com/mcp');
assert.equal(el(dialog, '#form-id').value, 'anysearch_3');
assert.equal(el(dialog, '#form-command').value, '');
assert.equal(el(dialog, '#form-env').value, '');
assert.match(el(dialog, '#form-headers').value, /Authorization: Bearer synthetic-anysearch-token/);
assert.ok(el(dialog, '[data-tab="json"]').classList.contains('active'), 'applying must preserve the active JSON tab');
assert.deepEqual(calls, ['ListMCPServers'], 'applying a template must not send a request');

// Switching providers clears pending credentials and keeps the old draft until apply.
const anysearchDraft = json(dialog);
await type(token, 'synthetic-pending-anysearch-token');
await el(dialog, '[data-mcp-preset="github"]').click();
assert.equal(token.value, '');
assert.deepEqual(json(dialog), anysearchDraft);
assert.equal(el(dialog, '[data-mcp-preset="anysearch"]').getAttribute('aria-pressed'), 'false');
assert.equal(el(dialog, '[data-mcp-preset="github"]').getAttribute('aria-pressed'), 'true');
await el(dialog, '#mcp-preset-apply').click();
assert.deepEqual(json(dialog), anysearchDraft, 'switching providers must require a fresh token');
await type(token, 'synthetic-github-token');
await el(dialog, '#mcp-preset-apply').click();
assert.deepEqual(json(dialog), {
  id: 'github', name: 'GitHub', enabled: true, transportType: 'streamable_http',
  url: 'https://api.githubcopilot.com/mcp/', headers: { Authorization: 'Bearer synthetic-github-token' },
});
assert.equal(el(dialog, '#form-url').value, 'https://api.githubcopilot.com/mcp/');
assert.doesNotMatch(el(dialog, '#form-headers').value, /Anysearch|anysearch-token/);
await el(dialog, '[data-tab="form"]').click();
assert.equal(el(dialog, 'input[name="form-transport"]:checked').value, 'streamable_http');
await el(dialog, '[data-tab="json"]').click();
assert.equal(json(dialog).url, 'https://api.githubcopilot.com/mcp/');
assert.equal(saved.length, 0);
await el(dialog, '#form-submit-btn').click();
assert.equal(saved.length, 1);
assert.equal(saved[0].method, 'AddMCPServer');
assert.equal(saved[0].body.id, 'github');
assert.equal(saved[0].body.url, 'https://api.githubcopilot.com/mcp/');
assert.deepEqual(saved[0].body.headers, { Authorization: 'Bearer synthetic-github-token' });
assert.equal(dialog.removed, true);

// Neither applied nor abandoned credentials may survive closing and reopening.
const abandoned = await openAdd();
assert.equal(el(abandoned, '#mcp-preset-token').value, '');
assert.doesNotMatch(el(abandoned, '#form-json').value, /synthetic-/);
await el(abandoned, '[data-mcp-preset="anysearch"]').click();
await type(el(abandoned, '#mcp-preset-token'), 'synthetic-abandoned-token');
await el(abandoned, '.dialog-close-btn').click();
const reopened = await openAdd();
assert.equal(el(reopened, '#mcp-preset-token').value, '');
assert.doesNotMatch(el(reopened, '#form-json').value, /synthetic-/);
await el(reopened, '.dialog-close-btn').click();

// Existing configuration stays editable without presets or a token requirement.
await el(root, '[data-action="edit-server"][data-id="anysearch"]').click();
const edit = dialogs.at(-1)!;
assert.equal(edit.querySelectorAll('[data-mcp-preset]').length, 0);
assert.equal(edit.querySelector('#mcp-preset-token'), null);
assert.equal(el(edit, '#form-url').value, 'https://existing.invalid/mcp');
assert.match(el(edit, '#form-headers').value, /synthetic-existing-token/);
await type(el(edit, '#form-name'), 'Existing renamed');
await el(edit, '#form-submit-btn').click();
assert.equal(saved.length, 2);
assert.equal(saved[1].method, 'UpdateMCPServer');
assert.equal(saved[1].body.id, 'anysearch');
assert.equal(saved[1].body.name, 'Existing renamed');
assert.equal(saved[1].body.url, 'https://existing.invalid/mcp');
assert.deepEqual(saved[1].body.headers, { Authorization: 'Bearer synthetic-existing-token' });

// Custom stdio setup remains available without selecting a provider.
const custom = await openAdd();
await type(el(custom, '#form-id'), 'custom_stdio');
await type(el(custom, '#form-name'), 'Custom stdio');
await type(el(custom, '#form-command'), 'node');
await el(custom, '#mcp-add-arg-btn').click();
await type(el(custom, '.arg-input'), 'server with spaces.js');
await type(el(custom, '#form-env'), 'CUSTOM_VALUE=synthetic-value');
await el(custom, '#form-submit-btn').click();
assert.equal(saved.length, 3);
assert.equal(saved[2].method, 'AddMCPServer');
assert.equal(saved[2].body.id, 'custom_stdio');
assert.equal(saved[2].body.transportType, 'stdio');
assert.equal(saved[2].body.command, 'node');
assert.deepEqual(saved[2].body.args, ['server with spaces.js']);
assert.deepEqual(saved[2].body.env, { CUSTOM_VALUE: 'synthetic-value' });
assert.ok(!notices.some((notice) => notice.includes('synthetic-')), 'notices must never expose credentials');
dispose();
console.log('PASS: MCP presets synchronize drafts, isolate tokens, and preserve custom/edit flows');
