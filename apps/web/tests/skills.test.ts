import assert from 'node:assert/strict';
import { createSkillsAPI } from '../src/api/skills';
import { instanceState } from '../src/instance-state';
import { mountSkillsPage } from '../src/pages/skills';
import { toast } from '../src/components/toast';

Object.assign(globalThis, {
  window: Object.assign(new EventTarget(), {
    location: { origin: 'http://localhost' },
  }),
  document: { querySelectorAll: () => [] },
  localStorage: { getItem: () => 'synthetic-token' },
});
const calls: {
  url: string;
  body?: BodyInit | null;
  method?: string;
  headers: Headers;
}[] = [];
globalThis.fetch = async (input, init) => {
  init?.signal?.throwIfAborted();
  calls.push({
    url: String(input),
    body: init?.body,
    method: init?.method,
    headers: new Headers(init?.headers),
  });
  return new Response('[]', { status: 200 });
};
instanceState.select({
  id: 'synthetic-a',
  name: 'A',
  enabled: false,
  created_at: '',
});
const stale = createSkillsAPI();
instanceState.select({
  id: 'synthetic-b',
  name: 'B',
  enabled: false,
  created_at: '',
});
await assert.rejects(stale.list());
assert.equal(calls.length, 0);
const api = createSkillsAPI();
const file = new File(['synthetic ZIP bytes'], 'skill.zip');
await api.importZIP(file);
assert.equal(calls[0].url, '/instances/synthetic-b/api/skills/import');
assert.equal(calls[0].body, file, 'ZIP bytes were converted into JSON');
assert.equal(calls[0].headers.get('Authorization'), 'Bearer synthetic-token');
await api.setEnabled('review', true);
assert.equal(calls[1].body, '{"enabled":true}');

class ElementMock {
  innerHTML = '';
  disabled = false;
  value = '';
  files?: File[];
  private children = new Map<string, ElementMock>();
  private listeners = new Map<string, EventListener>();
  querySelector(selector: string): ElementMock {
    if (!this.children.has(selector))
      this.children.set(selector, new ElementMock());
    return this.children.get(selector)!;
  }
  addEventListener(
    type: string,
    listener: EventListener,
    options?: AddEventListenerOptions,
  ) {
    this.listeners.set(type, listener);
    options?.signal?.addEventListener('abort', () =>
      this.listeners.delete(type),
    );
  }
  change() {
    this.listeners.get('change')?.({ target: this } as unknown as Event);
  }
}
const errors: string[] = [];
const successes: string[] = [];
toast.error = (message) => {
  errors.push(message);
};
toast.success = (message) => {
  successes.push(message);
};
const info = {
  name: 'review',
  description: '<script>unsafe</script>',
  enabled: false,
  manual_only: false,
  user_invocable: true,
  warnings: ['<img src=x>'],
  files: ['SKILL.md'],
  revision: 'test',
};
let resolveImport: ((result: (typeof info)[]) => void) | undefined;
let lists = 0;
const pageAPI = {
  ...api,
  async list() {
    lists++;
    return [info];
  },
  importZIP: () =>
    new Promise<(typeof info)[]>((resolve) => {
      resolveImport = resolve;
    }),
};
const root = new ElementMock();
const cleanup = mountSkillsPage(root as unknown as HTMLElement, pageAPI);
const settle = () => new Promise<void>((resolve) => setTimeout(resolve, 0));
await settle();
const list = root.querySelector('#skills-list');
assert.ok(list.innerHTML.includes('&lt;script&gt;unsafe&lt;/script&gt;'));
assert.ok(!list.innerHTML.includes('<img src=x>'));
const upload = root.querySelector('#skills-upload');
upload.files = [file];
upload.change();
await settle();
cleanup();
resolveImport?.([info]);
await settle();
assert.equal(lists, 1, 'unmounted import reloaded the page');
assert.equal(
  successes.length,
  0,
  'unmounted import showed a success notification',
);
assert.equal(errors.length, 0);
console.log(
  'Skills API isolation, ZIP upload, escaping and unmount checks passed',
);
