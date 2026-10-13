import assert from 'node:assert/strict';
import { create } from '@bufbuild/protobuf';
import { ModelRouterConfigurationSchema } from '@frostagent/proto';
import { mountPromptPage } from '../src/pages/prompt';
import { toast } from '../src/components/toast';
import type { createInstanceAPI } from '../src/api/client';

class MockElement {
  innerHTML = '';
  value = '';
  disabled = false;
  style: Record<string, string> = {};
  private listeners = new Map<string, (event?: Event) => unknown>();
  private children = new Map<string, MockElement>();
  private cardsHtml = '';
  private cards: MockElement[] = [];
  constructor(private attributes: Record<string, string> = {}) {}

  addEventListener(event: string, listener: (event?: Event) => unknown) {
    this.listeners.set(event, listener);
  }
  click() { return this.listeners.get('click')?.(); }
  input() { return this.listeners.get('input')?.(); }
  getAttribute(name: string) { return this.attributes[name] ?? null; }
  focus() {}
  querySelector(selector: string): MockElement {
    if (!this.children.has(selector)) this.children.set(selector, new MockElement());
    return this.children.get(selector)!;
  }
  querySelectorAll(selector: string): MockElement[] {
    if (selector !== '.prompt-session-card') return [];
    if (this.cardsHtml !== this.innerHTML) {
      this.cardsHtml = this.innerHTML;
      this.cards = [...this.innerHTML.matchAll(/data-session-id="([^"]+)"/g)]
        .map((match) => new MockElement({ 'data-session-id': match[1] }));
    }
    return this.cards;
  }
}

class MockDialog extends MockElement {
  closed = false;
  className = '';
  showModal() {}
  close() { this.closed = true; }
  remove() {}
  querySelectorAll(selector: string): MockElement[] {
    return selector === '.dialog-close-btn' ? [this.querySelector(selector)] : [];
  }
}

const dialogs: MockDialog[] = [];
const root = new MockElement();
Object.assign(globalThis, {
  document: {
    body: { appendChild() {} },
    createElement: (tag: string) => {
      const el = tag === 'dialog' ? new MockDialog() : new MockElement();
      if (el instanceof MockDialog) dialogs.push(el);
      return el;
    },
  },
});

const errors: string[] = [];
const successes: string[] = [];
toast.error = (message) => { errors.push(message); };
toast.success = (message) => { successes.push(message); };

const session = {
  sessionId: 'onebot:group:123', platform: 'onebot', messageCount: 2,
  createdAt: '', lastActive: '', groupSummary: '原有总结',
};
const pageCalls: Array<[number, string]> = [];
const contextCalls: string[] = [];
let deleteCalls = 0;
let modelStateCalls = 0;
const api = {
  async getModelRouterState() {
    modelStateCalls++;
    return { active: create(ModelRouterConfigurationSchema, { version: 1 }), loadError: '' };
  },
  async getSessions(size: number, token: string) {
    pageCalls.push([size, token]);
    return size === 100
      ? { sessions: [session, { ...session, sessionId: 'onebot:private:456', groupSummary: '' }], pagination: { total: 2, pageToken: '' } }
      : token === 'next'
      ? { sessions: [{ ...session, sessionId: 'onebot:private:456', groupSummary: '' }], pagination: { total: 2, pageToken: '' } }
      : { sessions: [session], pagination: { total: 2, pageToken: 'next' } };
  },
  async getSessionContext(id: string) {
    contextCalls.push(id);
    return { sessionId: id, platform: 'onebot', model: 'test-model', promptText: '{}', recentMessages: [] };
  },
  async deleteGroupSummary() {
    deleteCalls += 1;
    return deleteCalls === 1
      ? { success: false, error: 'store unavailable' }
      : { success: true, error: '' };
  },
} as unknown as ReturnType<typeof createInstanceAPI>;

const cleanup = mountPromptPage(root as unknown as HTMLElement, api);
await Promise.resolve();
assert.deepEqual(pageCalls, [[20, '']]);

const pagination = root.querySelector('#prompt-sessions-pagination');
pagination.querySelector('[data-action="next-page"]').click();
await Promise.resolve();
assert.deepEqual(pageCalls.at(-1), [20, 'next']);
pagination.querySelector('[data-action="prev-page"]').click();
await Promise.resolve();
assert.deepEqual(pageCalls.at(-1), [20, '']);

const search = root.querySelector('#prompt-session-search-input');
search.value = 'private:456';
search.input();
await Promise.resolve();
assert.deepEqual(pageCalls.at(-1), [100, undefined]);
assert.match(root.querySelector('#prompt-session-list').innerHTML, /onebot:private:456/);
assert.doesNotMatch(root.querySelector('#prompt-session-list').innerHTML, /data-session-id="onebot:group:123"/);
search.value = '';
search.input();

root.querySelector('#prompt-session-list').querySelectorAll('.prompt-session-card')[0].click();
await Promise.resolve();
const inspector = root.querySelector('#prompt-inspector-mount');
assert.ok(inspector.innerHTML.includes('id="pi-view-summary-btn"'), 'group summary action was not rendered');
assert.match(inspector.innerHTML, /id="pi-model-config-btn"/);
assert.doesNotMatch(inspector.innerHTML, /href="#\/model-router/);
assert.deepEqual(contextCalls, [session.sessionId]);

inspector.querySelector('#pi-model-config-btn').click();
await Promise.resolve();
const modelDialog = dialogs.at(-1)!;
assert.equal(modelStateCalls, 1, 'group model action did not load the current instance configuration');
assert.match(modelDialog.querySelector('.dialog-body').innerHTML, /select class="select group-model-binding"/);
modelDialog.querySelector('.dialog-close-btn').click();
assert.equal(modelDialog.closed, true);

inspector.querySelector('#pi-view-summary-btn').click();
const summaryDialog = dialogs.at(-1)!;
const deleteButton = summaryDialog.querySelector('#prompt-delete-summary-btn');
let pending = deleteButton.click();
dialogs.at(-1)!.querySelector('#confirm-ok-btn').click();
await pending;
assert.equal(deleteCalls, 1);
assert.equal(summaryDialog.closed, false);
assert.equal(deleteButton.disabled, false);
assert.match(errors.at(-1) || '', /store unavailable/);
assert.equal(session.groupSummary, '原有总结');

pending = deleteButton.click();
dialogs.at(-1)!.querySelector('#confirm-ok-btn').click();
await pending;
assert.equal(deleteCalls, 2);
assert.equal(summaryDialog.closed, true);
assert.equal(session.groupSummary, '');
assert.equal(successes.at(-1), '群聊总结已清空');
assert.deepEqual(pageCalls.at(-1), [20, '']);
assert.equal(contextCalls.length, 2);
cleanup();

session.groupSummary = '待删除的总结';
const nextRoot = new MockElement();
const cleanupWithPendingConfirm = mountPromptPage(nextRoot as unknown as HTMLElement, api);
await Promise.resolve();
nextRoot.querySelector('#prompt-session-list').querySelectorAll('.prompt-session-card')[0].click();
await Promise.resolve();
nextRoot.querySelector('#prompt-inspector-mount').querySelector('#pi-view-summary-btn').click();
const pendingDelete = dialogs.at(-1)!.querySelector('#prompt-delete-summary-btn').click();
const pendingConfirmation = dialogs.at(-1)!;
cleanupWithPendingConfirm();
await pendingDelete;
assert.equal(pendingConfirmation.closed, true);
assert.equal(deleteCalls, 2, 'unmounted page deleted a summary through a stale dialog');

process.stdout.write('PASS: merged page pagination, group actions, and deletion failure\n');
