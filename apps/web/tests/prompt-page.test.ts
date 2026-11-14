import assert from 'node:assert/strict';
import { createTestDOM, emit, query, settle } from './dom-fixture';
import { create } from '@bufbuild/protobuf';
import { ModelRouterConfigurationSchema } from '@frostagent/proto';
import { mountPromptPage } from '../src/pages/prompt';
import { instanceState } from '../src/instance-state';
import { toast } from '../src/components/toast';
import type { createInstanceAPI } from '../src/api/client';

const dom = createTestDOM();
const { dialogs } = dom;
try {
  const root = dom.root();
  instanceState.select({ id: 'synthetic-prompt-instance', name: 'Prompt fixture', enabled: true, created_at: '' });

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
    async getOverview() {
      return { botName: 'Configured Bot' };
    },
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

  const cleanup = mountPromptPage(root, api);
  await settle();
  assert.deepEqual(pageCalls, [[20, '']]);

  const pagination = query(root, '#prompt-sessions-pagination');
  query(pagination, '[data-action="next-page"]').click();
  await settle();
  assert.deepEqual(pageCalls.at(-1), [20, 'next']);
  query(pagination, '[data-action="prev-page"]').click();
  await settle();
  assert.deepEqual(pageCalls.at(-1), [20, '']);

  const search = query(root, '#prompt-session-search-input');
  search.value = 'private:456';
  emit(search, 'input');
  await settle();
  assert.deepEqual(pageCalls.at(-1), [100, undefined]);
  assert.match(
    query(root, '#prompt-session-list').innerHTML,
    /onebot:private:456/,
  );
  assert.doesNotMatch(
    query(root, '#prompt-session-list').innerHTML,
    /data-session-id="onebot:group:123"/,
  );
  search.value = '';
  emit(search, 'input');

  query(root, '#prompt-session-list')
    .querySelectorAll('.prompt-session-card')[0]
    .click();
  await settle();
  const inspector = query(root, '#prompt-inspector-mount');
  assert.ok(inspector.innerHTML.includes('id="pi-view-summary-btn"'), 'group summary action was not rendered');
  assert.match(inspector.innerHTML, /id="pi-model-config-btn"/);
  assert.doesNotMatch(inspector.innerHTML, /href="#\/model-router/);
  assert.deepEqual(contextCalls, [session.sessionId]);

  query(inspector, '#pi-model-config-btn').click();
  await settle();
  const modelDialog = dialogs.at(-1)!;
  assert.equal(modelStateCalls, 1, 'group model action did not load the current instance configuration');
  assert.match(
    query(modelDialog, '.dialog-body').innerHTML,
    /select class="select group-model-binding"/,
  );
  query(modelDialog, '.dialog-close-btn').click();
  assert.equal(!modelDialog.open, true);

  query(inspector, '#pi-view-summary-btn').click();
  const summaryDialog = dialogs.at(-1)!;
  const deleteButton = query(summaryDialog, '#prompt-delete-summary-btn');
  deleteButton.click();
  query(dialogs.at(-1)!, '#confirm-ok-btn').click();
  await settle();
  assert.equal(deleteCalls, 1);
  assert.equal(!summaryDialog.open, false);
  assert.equal(deleteButton.disabled, false);
  assert.match(errors.at(-1) || '', /store unavailable/);
  assert.equal(session.groupSummary, '原有总结');

  deleteButton.click();
  query(dialogs.at(-1)!, '#confirm-ok-btn').click();
  await settle();
  assert.equal(deleteCalls, 2);
  assert.equal(!summaryDialog.open, true);
  assert.equal(session.groupSummary, '');
  assert.equal(successes.at(-1), '群聊总结已清空');
  assert.deepEqual(pageCalls.at(-1), [20, '']);
  assert.equal(contextCalls.length, 2);
  cleanup();

  session.groupSummary = '待删除的总结';
  const nextRoot = dom.root();
  const cleanupWithPendingConfirm = mountPromptPage(nextRoot, api);
  await settle();
  query(nextRoot, '#prompt-session-list')
    .querySelectorAll('.prompt-session-card')[0]
    .click();
  await settle();
  query(
    query(nextRoot, '#prompt-inspector-mount'),
    '#pi-view-summary-btn',
  ).click();
  query(dialogs.at(-1)!, '#prompt-delete-summary-btn').click();
  const pendingConfirmation = dialogs.at(-1)!;
  cleanupWithPendingConfirm();
  await settle();
  assert.equal(!pendingConfirmation.open, true);
  assert.equal(deleteCalls, 2, 'unmounted page deleted a summary through a stale dialog');

  process.stdout.write('PASS: merged page pagination, group actions, and deletion failure\n');

  const privateSession = {
    ...session, sessionId: 'fixture:private:test_user', platform: 'fixture', groupSummary: 'stale private summary',
  };
  const privateRoot = dom.root();
  const privateCalls: string[] = [];
  const privateAPI = {
    ...api,
    async getSessions() {
      return { sessions: [session, privateSession], pagination: { total: 2, pageToken: '' } };
    },
    async getSessionContext(id: string) {
      privateCalls.push(id);
      return {
        sessionId: id, platform: 'fixture', runningSummary: 'fresh private compact',
        promptText: '{"messages":[]}',
        recentStructuredMessages: [{
          role: 'assistant', content: 'private reply', sender: '', senderId: '', messageId: '', time: '',
        }],
        summaryGroups: [{ summary: 'fresh private compact', messages: ['[user] User: private input'] }],
      };
    },
  } as unknown as ReturnType<typeof createInstanceAPI>;
  const cleanupPrivate = mountPromptPage(privateRoot, privateAPI);
  await settle();
  assert.match(privateRoot.innerHTML, /data-filter="all-private">全部私聊/);
  const privateTabs = Array.from(
    query(
      privateRoot,
      '#prompt-platform-filter-tabs',
    ).querySelectorAll<HTMLButtonElement>('[data-filter]'),
  );
  privateTabs.find((tab) => tab.getAttribute('data-filter') === 'all-private')!.click();
  await settle();
  const privateList = query(privateRoot, '#prompt-session-list');
  assert.match(privateList.innerHTML, /fixture:private:test_user/);
  assert.doesNotMatch(privateList.innerHTML, /onebot:group:123/);
  privateList.querySelectorAll('.prompt-session-card')[0].click();
  await settle();
  assert.deepEqual(privateCalls, [privateSession.sessionId]);
  const privateInspector = query(privateRoot, '#prompt-inspector-mount');
  assert.match(privateInspector.innerHTML, /当前私聊 compact 内容|private reply/);
  assert.match(privateInspector.innerHTML, />Configured Bot</, 'assistant history must show the configured bot name');
  assert.match(privateInspector.innerHTML, /href="#\/model-router"/);
  assert.doesNotMatch(privateInspector.innerHTML, /id="pi-model-config-btn"/);
  query(privateInspector, '#pi-view-summary-btn').click();
  const privateDialog = dialogs.at(-1)!;
  assert.match(privateDialog.innerHTML, /私聊总结/);
  assert.match(privateDialog.innerHTML, /fresh private compact/);
  assert.doesNotMatch(privateDialog.innerHTML, /prompt-delete-summary-btn/);
  assert.equal(deleteCalls, 2, 'private summary viewing must not call the group delete API');
  privateTabs.find((tab) => tab.getAttribute('data-filter') === 'all-groups')!.click();
  assert.doesNotMatch(privateList.innerHTML, /fixture:private:test_user/);
  assert.match(privateList.innerHTML, /onebot:group:123/);
  cleanupPrivate();
  process.stdout.write('PASS: private filter, context rendering, fresh read-only summary, and group-only actions\n');
  instanceState.select(null);
} finally {
  instanceState.select(null);
  dom.cleanup();
}
