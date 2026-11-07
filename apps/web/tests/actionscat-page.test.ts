import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers/promises';
import { mountActionsCatPage } from '../src/pages/actionscat';
import { instanceState } from '../src/instance-state';
import { toast } from '../src/components/toast';
import type { CreateMatcherRequest } from '../src/api/client';

class MockElement {
  private html = '';
  private children = new Map<string, MockElement>();
  private groups = new Map<string, MockElement[]>();
  private listeners = new Map<
    string,
    (event: { currentTarget: MockElement }) => unknown
  >();
  textContent = '';
  value = '';
  checked = false;
  disabled = false;
  hidden = false;
  className = '';
  style: Record<string, string> = {};
  dataset: Record<string, string> = {};

  get innerHTML() {
    return this.html;
  }
  set innerHTML(value: string) {
    this.html = value;
    this.children.clear();
    this.groups.clear();
  }
  addEventListener(
    name: string,
    listener: (event: { currentTarget: MockElement }) => unknown,
  ) {
    this.listeners.set(name, listener);
  }
  trigger(name: string) {
    return this.listeners.get(name)?.({ currentTarget: this });
  }
  querySelector(selector: string): MockElement {
    if (!this.children.has(selector))
      this.children.set(selector, new MockElement());
    return this.children.get(selector)!;
  }
  querySelectorAll(selector: string): MockElement[] {
    if (!this.groups.has(selector)) {
      const attribute = /^\[([\w-]+)(?:="([^"]*)")?\]$/.exec(selector);
      const matches: MockElement[] = [];
      if (attribute) {
        for (const [, attributes] of this.html.matchAll(/<button\b([^>]*)>/g)) {
          const value = new RegExp(`\\b${attribute[1]}="([^"]*)"`).exec(
            attributes,
          )?.[1];
          if (
            value === undefined ||
            (attribute[2] !== undefined && value !== attribute[2])
          )
            continue;
          const button = new MockElement();
          for (const [, name, data] of attributes.matchAll(
            /data-([\w-]+)="([^"]*)"/g,
          )) {
            button.dataset[
              name.replace(/-([a-z])/g, (_, letter: string) =>
                letter.toUpperCase(),
              )
            ] = data;
          }
          matches.push(button);
        }
      }
      this.groups.set(selector, matches);
    }
    return this.groups.get(selector)!;
  }
  focus() {}
  showModal() {}
  close() {}
  remove() {}
}

const dialogs: MockElement[] = [];
Object.assign(globalThis, {
  document: {
    querySelectorAll: () => [],
    createElement: () => new MockElement(),
    body: { appendChild: (element: MockElement) => dialogs.push(element) },
  },
  window: Object.assign(new EventTarget(), {
    location: { origin: 'http://localhost' },
  }),
});
instanceState.select({
  id: 'console-test-instance',
  name: 'Test',
  enabled: true,
  created_at: '',
});
toast.success = () => {};
const warnings: string[] = [];
toast.warning = (message) => {
  warnings.push(message);
};
toast.error = (message) => {
  throw new Error(message);
};

const status = {
  configured: true,
  healthy: true,
  authenticated: true,
  endpoint: 'http://test.invalid',
};
const action = {
  id: 'synthetic-action',
  name: 'Synthetic action',
  description: '',
  enabled: true,
};
const calls: string[] = [];
function useFetch(
  handler: (url: string, init?: RequestInit) => Response | Promise<Response>,
) {
  calls.length = 0;
  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = input instanceof Request ? input.url : String(input);
    calls.push(url);
    return handler(url, init);
  };
}
const settle = async () => {
  for (let i = 0; i < 5; i++) await setImmediate();
};
function deferredResponse() {
  let resolve!: (response: Response) => void;
  const promise = new Promise<Response>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
function mount() {
  const root = new MockElement();
  const dispose = mountActionsCatPage(root as unknown as HTMLElement);
  return { root, dispose };
}

// Mount the real page and hold the action response to exercise the dependency.
{
  const pendingActions = deferredResponse();
  useFetch((url) => {
    if (url.endsWith('/status')) return Response.json(status);
    if (url.endsWith('/actions')) return pendingActions.promise;
    const id = url.includes('/synthetic-action/') ? action.id : 'second-action';
    assert.ok(
      url.includes(`/actions/${id}/runs?`),
      `unexpected request: ${url}`,
    );
    return Response.json([
      {
        id: id === action.id ? 'older-run' : 'newer-run',
        action_id: id,
        status: 'succeeded',
        trigger_type: 'manual',
        duration_ms: 10,
        created_at:
          id === action.id ? '2026-01-01T00:00:00Z' : '2026-01-02T00:00:00Z',
      },
    ]);
  });
  const { root, dispose } = mount();
  await settle();
  assert.equal(
    calls.length,
    2,
    'run loading must wait for the action response',
  );
  pendingActions.resolve(
    Response.json([action, { ...action, id: 'second-action' }]),
  );
  await settle();
  assert.equal(
    calls.filter((url) => url.includes('/runs?')).length,
    2,
    'initial mount must query each action history',
  );
  assert.equal(root.querySelector('#actionscat-runs-count').textContent, '2');
  const html = root.querySelector('#actionscat-runs-container').innerHTML;
  assert.match(html, /newer-run/);
  assert.match(html, /older-run/);
  assert.ok(
    html.indexOf('newer-run') < html.indexOf('older-run'),
    'initial history must be sorted newest first',
  );
  dispose();
}

// Open the actual trigger dialog through the rendered action's click handler.
{
  const captureKey = '<img src=x onerror="alert(1)">&';
  const captureValue = '<svg onload="alert(2)">&';
  const saved: CreateMatcherRequest[] = [];
  useFetch((url, init) => {
    if (url.endsWith('/status')) return Response.json(status);
    if (url.endsWith('/actions')) return Response.json([action]);
    if (url.endsWith('/matchers')) {
      if (init?.method === 'POST') {
        saved.push(JSON.parse(String(init.body)) as CreateMatcherRequest);
        return Response.json({ id: 'created-matcher' });
      }
      return Response.json([
        {
          id: 'synthetic-matcher',
          name: 'Capture display',
          pattern: '(?P<name>.*)',
          match_type: 'regex',
          target_field: 'text',
          priority: 0,
          enabled: true,
          capture_env_map: { [captureKey]: captureValue, name: 'MATCH_NAME' },
        },
      ]);
    }
    assert.ok(
      url.includes('/runs?') || url.endsWith('/schedules'),
      `unexpected request: ${url}`,
    );
    return Response.json([]);
  });
  const { root, dispose } = mount();
  await settle();
  const buttons = root
    .querySelector('#actionscat-actions-container')
    .querySelectorAll('[data-action="manage-triggers"]');
  assert.equal(buttons.length, 1);
  buttons[0].trigger('click');
  await settle();
  const dialog = dialogs.at(-1)!;
  assert.ok(dialog, 'trigger button must open the real dialog');
  const html = dialog.querySelector('#dialog-matchers-list').innerHTML;
  assert.ok(
    html.includes(
      '&lt;img src=x onerror=&quot;alert(1)&quot;&gt;&amp;→&lt;svg onload=&quot;alert(2)&quot;&gt;&amp;',
    ),
    'both capture names and target variables must be escaped',
  );
  assert.ok(!html.includes(captureKey) && !html.includes(captureValue));
  assert.ok(
    html.includes('name→MATCH_NAME'),
    'ordinary capture mappings remain readable',
  );

  for (const matchType of ['contains', 'exact', 'regex']) {
    dialog.querySelector('#matcher-name-input').value =
      'Whitespace-sensitive matcher';
    dialog.querySelector('#matcher-pattern-input').value = ' cat ';
    dialog.querySelector('#matcher-type-select').value = matchType;
    await dialog.querySelector('#matcher-add-btn').trigger('click');
    assert.equal(
      saved.at(-1)?.pattern,
      ' cat ',
      `${matchType} pattern must reach the API without trimming`,
    );
    assert.equal(saved.at(-1)?.match_type, matchType);
  }
  dialog.querySelector('#matcher-name-input').value = 'Empty matcher';
  dialog.querySelector('#matcher-pattern-input').value = ' \t ';
  await dialog.querySelector('#matcher-add-btn').trigger('click');
  assert.equal(
    saved.length,
    3,
    'whitespace-only patterns must still be rejected',
  );
  assert.equal(warnings.at(-1), '请输入匹配表达式 (Pattern)');
  dialog.querySelector('#dialog-triggers-close').trigger('click');
  dispose();
}

for (const failStatus of [false, true]) {
  useFetch((url) => {
    assert.ok(
      url.endsWith('/status'),
      'disabled or failed status must not query actions or runs',
    );
    return failStatus
      ? Response.json({ error: 'synthetic status failure' }, { status: 503 })
      : Response.json({ ...status, disabled: true });
  });
  const { root, dispose } = mount();
  await settle();
  assert.equal(calls.length, 1);
  assert.match(
    root.querySelector('#actionscat-status-container').innerHTML,
    failStatus ? /尚未配置/ : /ActionsCat已被FrostAgent管理员禁用。/,
  );
  dispose();
}

// Disposing while either sequential dependency is pending must stop later work.
for (const pendingStep of ['status', 'actions']) {
  const pending = deferredResponse();
  useFetch((url) => {
    if (url.endsWith(`/${pendingStep}`)) return pending.promise;
    assert.ok(
      url.endsWith('/status'),
      `unexpected request after disposal: ${url}`,
    );
    return Response.json(status);
  });
  const { root, dispose } = mount();
  await settle();
  const requestCount = calls.length;
  const actionsHTML = root.querySelector(
    '#actionscat-actions-container',
  ).innerHTML;
  const runsHTML = root.querySelector('#actionscat-runs-container').innerHTML;
  dispose();
  pending.resolve(Response.json(pendingStep === 'status' ? status : [action]));
  await settle();
  assert.equal(
    calls.length,
    requestCount,
    `disposing during ${pendingStep} must stop subsequent requests`,
  );
  assert.equal(
    root.querySelector('#actionscat-actions-container').innerHTML,
    actionsHTML,
  );
  assert.equal(
    root.querySelector('#actionscat-runs-container').innerHTML,
    runsHTML,
  );
}

// An action-list failure should remain visible and must not invent run requests.
{
  useFetch((url) => {
    if (url.endsWith('/status')) return Response.json(status);
    assert.ok(url.endsWith('/actions'), `unexpected request: ${url}`);
    return Response.json(
      { error: 'synthetic action failure' },
      { status: 503 },
    );
  });
  const { root, dispose } = mount();
  await settle();
  assert.equal(calls.length, 2);
  assert.match(
    root.querySelector('#actionscat-actions-container').innerHTML,
    /synthetic action failure/,
  );
  dispose();
}

console.log(
  'ActionsCat page lifecycle, capture escaping, and matcher pattern checks passed.',
);
