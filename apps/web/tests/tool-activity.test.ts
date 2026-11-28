import { formatMessageTime } from '../src/utils/formatters';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { fromJson } from '@bufbuild/protobuf';
import { GetSessionContextResponseSchema } from '@frostagent/proto';
import groupToolContextFixture from './fixtures/group-tool-context.json';
import privateTimedContextFixture from './fixtures/private-timed-context.json';
import {
  buildInspectorDataFromSessionContext,
  mergeSessionToolActivity,
  parsePrompt,
  renderPromptInspector,
  sessionToolActivitySourceIds,
} from '../src/components/prompt-inspector';
import {
  extractToolActivityBlock,
  parseToolHistoryNote,
  renderToolActivity,
  type ToolArgumentsLoader,
} from '../src/components/tool-activity';

const { JSDOM } = createRequire(resolve('package.json'))('jsdom');
const dom = new JSDOM('<div id="inspector"></div>');
const container = dom.window.document.querySelector(
  '#inspector',
) as HTMLElement;
const originalRef = 'fa_toolcall_550e8400-e29b-41d4-a716-446655440000';
const readRef = 'fa_toolcall_550e8400-e29b-41d4-a716-446655440001';
const legacy = `【工具调用记录；原文可用 read_tool_history 回查】 来源消息: "fixture-source"\nsend_message (ref_id: ${originalRef})`;
const current = `【工具执行记录】\n来源消息: "fixture-source"\n- 工具调用: send_message (ref_id: ${originalRef})\n- 历史回查: read_tool_history (ref_id: ${readRef}) (read_target_ref_id: ${originalRef})`;

assert.equal(parseToolHistoryNote(legacy)?.entries.length, 1);
assert.equal(
  parseToolHistoryNote(current)?.entries[1].readTargetRefId,
  originalRef,
);
const notice =
  '此为内部占位元数据，由后端自动生成，非常规Assistant输出结果，禁止模仿该格式输出！';
assert.equal(
  parseToolHistoryNote(`${current}\n${notice}`)?.entries.length,
  2,
);
for (const content of [
  'I can use read_tool_history',
  notice,
  `send_message (ref_id: ${originalRef})`,
  `${legacy}\nordinary quoted text`,
  '【工具执行记录】\nnot an entry',
]) {
  assert.equal(parseToolHistoryNote(content), undefined, content);
}

for (const structured of [true, false]) {
  const messages = [
    { role: 'assistant', content: legacy },
    { role: 'assistant', content: 'fixture bot reply' },
    { role: 'user', content: legacy },
    { role: 'assistant', content: current, toolHistoryRunId: 'fixture-run' },
  ];
  const parsed = buildInspectorDataFromSessionContext({
    sessionId: 'private:fixture-user',
    ...(structured
      ? {
          recentStructuredMessages: messages.map((message) => ({
            ...message,
            sender: '',
            senderId: '',
            messageId: '',
            time: '',
          })),
        }
      : { history: messages }),
  });
  renderPromptInspector(container, parsed);
  const activities = container.querySelectorAll<HTMLDetailsElement>(
    '[data-tool-activity]',
  );
  assert.equal(
    activities.length,
    2,
    'legacy and current tool notes have independent cards',
  );
  assert.equal(
    activities[0].querySelector('.tool-activity-read-badge'),
    null,
    'a retrieval hint is not an actual read',
  );
  assert.equal(
    activities[1].querySelectorAll('[data-kind="history-read"]').length,
    1,
  );
  assert.match(
    activities[1].querySelector('summary')!.textContent!,
    /1 次工具调用 · 1 次历史回查/,
  );
  for (const activity of activities) {
    assert.equal(activity.open, false, 'tool runs should start collapsed');
    assert.equal(
      activity.querySelector('.msg-role-badge'),
      null,
      'internal records are not Bot speech',
    );
    assert.doesNotMatch(
      activity.querySelector('summary')!.textContent!,
      /fa_toolcall_/,
    );
  }
  assert.match(activities[1].textContent!, new RegExp(originalRef));
  assert.match(activities[1].textContent!, new RegExp(readRef));
  assert.equal(
    container.querySelectorAll('.group-msg-row.is-assistant').length,
    1,
  );
  assert.equal(
    container.querySelector('.group-msg-row.is-user .msg-content')!.textContent,
    legacy,
    'user text stays opaque',
  );
}

const raw = JSON.stringify({
  model: 'fixture-model',
  messages: [
    { role: 'system', content: 'fixture system' },
    { role: 'user', content: 'fixture request' },
    { role: 'assistant', content: legacy },
    {
      role: 'assistant',
      content: null,
      tool_calls: [
        {
          id: 'fixture-read',
          type: 'function',
          function: {
            name: 'read_tool_history',
            arguments: JSON.stringify({ read_target_ref_id: originalRef }),
          },
        },
      ],
    },
    {
      role: 'tool',
      tool_call_id: 'fixture-read',
      content: '<img src=x onerror=alert(1)> fixture original',
    },
    { role: 'assistant', content: 'fixture answer' },
  ],
});
const parsed = parsePrompt(raw);
assert.equal(
  parsed.requestMessages?.length,
  5,
  'all request turns must remain in order',
);
renderPromptInspector(container, parsed, { showRawToggle: true });
assert.equal(container.querySelectorAll('[data-tool-activity]').length, 3);
assert.match(container.textContent!, /历史回查/);
assert.match(container.textContent!, /回查返回/);
assert.match(container.textContent!, /fixture-read/);
assert.equal(container.querySelector('img'), null, 'tool results are escaped');
(container.querySelector('#pi-toggle-view-btn') as HTMLButtonElement).click();
assert.equal(
  container.querySelector('pre')!.textContent,
  raw,
  'raw view must preserve exact tool calls/results',
);

// An unknown future annotation is still displayed as an internal record, using API metadata.
renderPromptInspector(
  container,
  buildInspectorDataFromSessionContext({
    sessionId: 'private:fixture-user',
    history: [
      {
        role: 'assistant',
        content: '<script>unknown format</script>',
        toolHistoryRunId: 'fixture-run',
      },
    ],
  }),
);
assert.equal(container.querySelectorAll('[data-tool-activity]').length, 1);
assert.equal(container.querySelector('script'), null);
assert.equal(container.querySelector('.msg-role-badge'), null);

const queryArguments = '{ "query": "好奇 探头", "id":7700000000000000001 }';
const readArguments = `{"read_target_ref_id":"${originalRef}"}`;
const calls: string[] = [];
const load: ToolArgumentsLoader = async (refId) => {
  calls.push(refId);
  return {
    refId,
    tool: refId === originalRef ? 'send_message' : 'read_tool_history',
    argumentsRaw: refId === originalRef ? queryArguments : readArguments,
    argumentsAvailable: true,
  };
};
const data = buildInspectorDataFromSessionContext({
  sessionId: 'private:fixture-user',
  history: [{ role: 'assistant', content: current }],
});
let cleanup = renderPromptInspector(container, data, {
  loadToolArguments: load,
});
assert.equal(calls.length, 0, 'parameters must not load before expansion');
assert.equal(container.querySelectorAll('.tool-activity-ref').length, 2);
assert.equal(
  container.querySelectorAll('.tool-activity-ref')[0].textContent,
  `(${originalRef})`,
);
assert.equal(
  container.querySelectorAll('.tool-activity-ref')[1].textContent,
  `(${readRef})`,
);
assert.doesNotMatch(
  container.textContent!,
  /完整引用|本次引用|读取的引用|读取 →/,
);
const flush = () => new Promise((resolve) => setTimeout(resolve, 15));
for (const [refId, args] of [
  [originalRef, queryArguments],
  [readRef, readArguments],
]) {
  const details = container.querySelector(
    `details[data-tool-ref="${refId}"]`,
  ) as HTMLDetailsElement;
  assert.equal(details.querySelector('summary')!.textContent, '详细参数');
  details.open = true;
  await flush();
  assert.equal(
    details.querySelector('pre')!.textContent,
    args,
    'raw JSON, spaces and large integers must remain exact',
  );
  details.open = false;
  await flush();
  details.open = true;
  await flush();
}
assert.deepEqual(
  calls,
  [originalRef, readRef],
  'each archived call should load once',
);
cleanup();

const resultLoads: string[] = [];
cleanup = renderPromptInspector(container, data, {
  loadToolArguments: async (refId, options) => {
    resultLoads.push(
      `${refId}:${options?.includeResult ? 'result' : 'args'}:${options?.resultOffset ?? 0}`,
    );
    if (!options?.includeResult)
      return {
        refId,
        tool: 'send_message',
        argumentsRaw: queryArguments,
        argumentsAvailable: true,
      };
    const offset = options.resultOffset ?? 0;
    const pages = ['{"orderIdStr":"7700000000000000001"} ', '<b>fixture</b>'];
    return {
      refId,
      tool: 'send_message',
      argumentsRaw: '',
      argumentsAvailable: false,
      resultRaw: pages[offset] ?? '',
      resultAvailable: true,
      resultHasMore: offset === 0,
      resultNextOffset: offset === 0 ? 1 : 0,
    };
  },
});
const resultDetails = container.querySelector(
  `details[data-tool-field="result"][data-tool-ref="${originalRef}"]`,
) as HTMLDetailsElement;
assert.equal(
  resultDetails.querySelector('summary')!.textContent,
  '工具执行结果',
);
assert.equal(resultDetails.querySelector('pre')!.textContent, '展开后加载结果');
assert.equal(
  container.querySelectorAll('details[data-tool-field="result"]').length,
  2,
  'every archived call should offer a result disclosure',
);
assert.deepEqual(resultLoads, [], 'results must not load before expansion');
const argumentsDetails = container.querySelector(
  `details[data-tool-ref="${originalRef}"]:not([data-tool-field])`,
) as HTMLDetailsElement;
argumentsDetails.open = true;
await flush();
resultDetails.open = true;
await flush();
assert.equal(
  resultDetails.querySelector('pre')!.textContent,
  '{"orderIdStr":"7700000000000000001"} ',
  'first result page only',
);
const more = resultDetails.querySelector('.tool-activity-more') as HTMLButtonElement;
assert.equal(more.hidden, false, 'more button shown while pages remain');
more.click();
await flush();
assert.equal(
  resultDetails.querySelector('pre')!.textContent,
  '{"orderIdStr":"7700000000000000001"} <b>fixture</b>',
  'next page appends as text, not markup',
);
assert.equal(resultDetails.querySelector('b'), null);
assert.equal(more.hidden, true, 'more button hidden after the last page');
assert.deepEqual(
  resultLoads,
  [`${originalRef}:args:0`, `${originalRef}:result:0`, `${originalRef}:result:1`],
  'arguments never request the result; result pages load on demand',
);
cleanup();

renderPromptInspector(container, data, {
  loadToolArguments: async (refId) => ({
    refId,
    tool: 'send_message',
    argumentsRaw: queryArguments,
    argumentsAvailable: true,
    resultRaw: '',
    resultAvailable: false,
  }),
});
const quarantinedResult = container.querySelector(
  `details[data-tool-field="result"][data-tool-ref="${originalRef}"]`,
) as HTMLDetailsElement;
quarantinedResult.open = true;
await flush();
assert.equal(
  quarantinedResult.querySelector('pre')!.textContent,
  '执行结果已隔离，无法展示',
);

let attempt = 0;
renderPromptInspector(container, data, {
  loadToolArguments: async (refId) => {
    if (attempt++ === 0) throw new Error('fixture outage');
    return {
      refId,
      tool: 'send_message',
      argumentsRaw: '<script>fixture</script>',
      argumentsAvailable: true,
    };
  },
});
let detail = container.querySelector(
  'details[data-tool-ref]',
) as HTMLDetailsElement;
detail.open = true;
await flush();
assert.match(detail.textContent!, /参数加载失败/);
const retry = detail.querySelector('button') as HTMLButtonElement;
assert.equal(retry.hidden, false);
retry.click();
await flush();
assert.equal(
  detail.querySelector('pre')!.textContent,
  '<script>fixture</script>',
);
assert.equal(detail.querySelector('script'), null);

renderPromptInspector(container, data, {
  loadToolArguments: async (refId) => ({
    refId,
    tool: 'send_message',
    argumentsRaw: '',
    argumentsAvailable: false,
  }),
});
detail = container.querySelector(
  'details[data-tool-ref]',
) as HTMLDetailsElement;
detail.open = true;
await flush();
assert.equal(detail.querySelector('pre')!.textContent, '参数已隔离，无法展示');

let release!: (value: Awaited<ReturnType<ToolArgumentsLoader>>) => void;
cleanup = renderPromptInspector(container, data, {
  loadToolArguments: () =>
    new Promise((resolve) => {
      release = resolve;
    }),
});
detail = container.querySelector(
  'details[data-tool-ref]',
) as HTMLDetailsElement;
detail.open = true;
await flush();
cleanup();
container.innerHTML = '<p>next session</p>';
release({
  refId: originalRef,
  tool: 'send_message',
  argumentsRaw: 'stale parameters',
  argumentsAvailable: true,
});
await flush();
assert.equal(
  container.textContent,
  'next session',
  'old requests must not write into the next inspector',
);

const nativeRaw = JSON.stringify({
  messages: [
    {
      role: 'assistant',
      tool_calls: [
        {
          id: 'fixture-native',
          function: { name: 'send_message', arguments: queryArguments },
        },
      ],
    },
    {
      role: 'tool',
      tool_call_id: 'fixture-native',
      content: `fixture result\n(ref_id: ${originalRef})`,
    },
  ],
});
renderPromptInspector(container, parsePrompt(nativeRaw));
assert.equal(
  container.querySelectorAll('.tool-activity-ref')[0].textContent,
  `(${originalRef})`,
  'native call should recover the archived UUID from its paired result',
);
assert.equal(
  container.querySelector('.tool-activity-arguments')!.textContent,
  queryArguments,
);
// Decode the real GetSessionContext wire fixture as the Connect client does.
const groupContext = fromJson(
  GetSessionContextResponseSchema,
  groupToolContextFixture,
);
const groupPrompt = buildInspectorDataFromSessionContext(groupContext);
assert.equal(groupPrompt.recentMessages.length, 3);
assert.equal(groupPrompt.recentMessages[0].id, 'fixture-message');
assert.equal(groupPrompt.recentMessages[1].toolHistoryRunId, 'fixture-run');
assert.equal(groupPrompt.recentMessages[2].content, 'fixture group answer');
let groupLoads = 0;
cleanup = renderPromptInspector(container, groupPrompt, {
  loadToolArguments: async (refId) => {
    groupLoads++;
    return {
      refId,
      tool: 'send_sticker',
      argumentsRaw: queryArguments,
      argumentsAvailable: true,
    };
  },
});
assert.equal(container.querySelector('[data-group-tool-records]'), null);
assert.doesNotMatch(container.textContent!, /群聊工具执行记录/);
const groupSource = container.querySelector('[data-msg-id="fixture-message"]')!;
const groupToolCard = groupSource.nextElementSibling!;
assert.ok(groupToolCard.matches('[data-tool-activity]'));
const collapsedGroupCard = groupToolCard.outerHTML;
assert.equal(container.querySelectorAll('[data-tool-activity]').length, 1);
assert.equal(
  groupToolCard.querySelector('.msg-role-badge'),
  null,
  'tool notes must not become delivered Bot replies',
);
assert.equal(
  groupToolCard.nextElementSibling!.querySelector('.msg-content')!.textContent,
  'fixture group answer',
  'source message, tool activity and reply must share one timeline',
);
assert.match(container.textContent!, /2 条未压缩消息/);
assert.equal(
  [...container.querySelectorAll('.msg-content')].filter(
    (el) => el.textContent === 'fixture group answer',
  ).length,
  1,
  'public replies must not be duplicated from history',
);
assert.doesNotMatch(container.textContent!, /fixture internal trigger/);
assert.match(groupToolCard.textContent!, new RegExp(originalRef));
(groupToolCard as HTMLDetailsElement).open = true;
const groupDetails = groupToolCard.querySelector(
  'details[data-tool-ref]',
) as HTMLDetailsElement;
groupDetails.open = true;
await flush();
assert.equal(groupLoads, 1);
assert.equal(groupDetails.querySelector('pre')!.textContent, queryArguments);
cleanup();

// The same run must use identical markup in group and private conversations.
renderPromptInspector(
  container,
  buildInspectorDataFromSessionContext({
    sessionId: 'private:fixture-user',
    history: groupContext.history,
  }),
);
assert.equal(
  container.querySelector('[data-tool-activity]')!.outerHTML,
  collapsedGroupCard,
);

const oldRef = 'fa_toolcall_550e8400-e29b-41d4-a716-446655440002';
const missingRef = 'fa_toolcall_550e8400-e29b-41d4-a716-446655440003';
const sourceLessRef = 'fa_toolcall_550e8400-e29b-41d4-a716-446655440004';
const note = (source: string | undefined, refId: string) =>
  `【工具执行记录】${source === undefined ? '' : `\n来源消息: ${JSON.stringify(source)}`}\n- 工具调用: send_message (ref_id: ${refId})`;
const oldMessage = {
  ...groupContext.recentStructuredMessages[0],
  messageId: 'fixture-old',
  content: 'fixture old question',
};
const mergedGroup = buildInspectorDataFromSessionContext({
  ...groupContext,
  summaryGroups: [
    { summary: 'fixture compact', structuredMessages: [oldMessage] },
  ],
  recentStructuredMessages: [
    ...groupContext.recentStructuredMessages,
    {
      ...groupContext.recentStructuredMessages[0],
      messageId: 'fixture-unrelated',
      content: 'fixture unrelated chatter',
    },
  ],
  history: [
    {
      role: 'assistant',
      content: note('fixture-old', oldRef),
      toolHistoryRunId: 'fixture-old-run',
    },
    ...groupContext.history,
    {
      role: 'assistant',
      content: `【工具执行记录】\n来源消息: "fixture-message"\n- 历史回查: read_tool_history (ref_id: ${readRef}) (read_target_ref_id: ${originalRef})`,
      toolHistoryRunId: 'fixture-read-run',
    },
    { role: 'assistant', content: note('fixture-missing', missingRef) },
    { role: 'assistant', content: note(undefined, sourceLessRef) },
    // Replayed entries must not duplicate either runs or legacy references.
    groupContext.history[1],
    { role: 'assistant', content: note('fixture-old', oldRef) },
    { role: 'user', content: note('fixture-unrelated', missingRef) },
  ],
});
renderPromptInspector(container, mergedGroup);
const compactRows = container.querySelector('.summary-group-messages')!;
const compactToolCard = compactRows.querySelector('[data-tool-activity]')!;
assert.equal(
  compactToolCard.previousElementSibling!.getAttribute('data-msg-id'),
  'fixture-old',
);
assert.match(compactToolCard.textContent!, new RegExp(oldRef));
assert.match(
  container.querySelector('.summary-compact-card-meta')!.textContent!,
  /1 条消息/,
);
const recentSource = container.querySelector(
  '[data-msg-id="fixture-message"]',
)!;
assert.match(
  recentSource.nextElementSibling!.textContent!,
  new RegExp(originalRef),
);
assert.match(
  recentSource.nextElementSibling!.nextElementSibling!.textContent!,
  new RegExp(readRef),
);
assert.equal(
  recentSource.nextElementSibling!.nextElementSibling!.nextElementSibling!.querySelector(
    '.msg-content',
  )!.textContent,
  'fixture group answer',
);
assert.equal(container.querySelectorAll('[data-tool-activity]').length, 5);
assert.match(container.textContent!, /3 条未压缩消息/);
const unlocatedCards = [
  ...container.querySelectorAll('[data-tool-activity]'),
].filter((card) => /位置未定位/.test(card.textContent!));
assert.equal(
  unlocatedCards.length,
  2,
  'missing and legacy source-less runs remain visible',
);
assert.match(unlocatedCards[0].textContent!, /来源消息不在当前窗口/);
assert.match(unlocatedCards[1].textContent!, /未记录来源消息/);
assert.equal(
  container.querySelector('[data-msg-id="fixture-unrelated"]')!
    .nextElementSibling,
  null,
  'unrelated chatter must not acquire a tool record',
);
assert.doesNotMatch(container.textContent!, /fixture internal trigger/);

const toolsOnlyGroup = buildInspectorDataFromSessionContext({
  sessionId: 'group:fixture-empty',
  history: [{ role: 'assistant', content: note(undefined, sourceLessRef) }],
});
renderPromptInspector(container, toolsOnlyGroup);
assert.equal(container.querySelectorAll('[data-tool-activity]').length, 1);
assert.equal(container.querySelectorAll('.group-msg-row').length, 0);

// An inline run supplied by a future/legacy server is not projected twice.
const alreadyInline = buildInspectorDataFromSessionContext({
  ...groupContext,
  recentStructuredMessages: [
    groupContext.recentStructuredMessages[0],
    {
      role: 'assistant',
      content: groupContext.history[1].content,
      toolHistoryRunId: 'fixture-run',
      sender: '',
      senderId: '',
      messageId: '',
      time: '',
    },
    groupContext.recentStructuredMessages[1],
  ],
});
renderPromptInspector(container, alreadyInline);
assert.equal(container.querySelectorAll('[data-tool-activity]').length, 1);

const privateTimedContext = fromJson(
  GetSessionContextResponseSchema,
  privateTimedContextFixture,
);
renderPromptInspector(
  container,
  buildInspectorDataFromSessionContext(privateTimedContext),
);
assert.equal(
  container.querySelector('.group-msg-row.is-user .msg-time')!.textContent,
  formatMessageTime('2026-11-17 20:34:34'),
);
assert.equal(
  container.querySelector('.group-msg-row.is-assistant .msg-time')!.textContent,
  formatMessageTime('2026-11-17 20:34:40'),
);
assert.equal(
  container.querySelector('.group-msg-row.is-user .msg-content')!.textContent,
  'fixture input',
);
assert.equal(
  container.querySelector('.group-msg-row.is-assistant .msg-content')!
    .textContent,
  'fixture reply',
);

let executionState = 'running';
let pendingLoads = 0;
cleanup = renderPromptInspector(container, data, {
  loadToolArguments: async (refId) => {
    pendingLoads++;
    return {
      refId,
      tool: 'send_message',
      argumentsRaw: executionState === 'complete' ? queryArguments : '',
      argumentsAvailable: executionState === 'complete',
      argumentsPending: executionState !== 'complete',
    };
  },
});
detail = container.querySelector(
  'details[data-tool-ref]',
) as HTMLDetailsElement;
detail.open = true;
await flush();
assert.equal(detail.dataset.argumentsState, 'pending');
assert.doesNotMatch(detail.querySelector('pre')!.textContent!, /隔离/);
assert.equal(
  (detail.querySelector('button') as HTMLButtonElement).hidden,
  false,
);
executionState = 'executed';
detail.open = false;
await flush();
detail.open = true;
await flush();
assert.equal(pendingLoads, 2, 'reopening a pending record must fetch again');
assert.equal(detail.dataset.argumentsState, 'pending');
executionState = 'complete';
(detail.querySelector('button') as HTMLButtonElement).click();
await flush();
assert.equal(
  pendingLoads,
  3,
  'pending records must also support an explicit retry',
);
assert.equal(detail.querySelector('pre')!.textContent, queryArguments);
assert.equal(detail.dataset.argumentsState, 'loaded');
detail.open = false;
await flush();
detail.open = true;
await flush();
assert.equal(pendingLoads, 3, 'final reviewed parameters may still be cached');
cleanup();


let pageTwoAttempts = 0;
cleanup = renderPromptInspector(container, data, {
  loadToolArguments: async (refId, options) => {
    if (!options?.includeResult)
      return { refId, tool: 'send_message', argumentsRaw: '', argumentsAvailable: false };
    const offset = options.resultOffset ?? 0;
    if (offset === 1 && pageTwoAttempts++ === 0) throw new Error('fixture page outage');
    return {
      refId,
      tool: 'send_message',
      argumentsRaw: '',
      argumentsAvailable: false,
      resultRaw: ['第一页 ', '第二页'][offset] ?? '',
      resultAvailable: true,
      resultHasMore: offset === 0,
      resultNextOffset: offset === 0 ? 1 : 0,
    };
  },
});
const pagedDetails = container.querySelector(
  `details[data-tool-field="result"][data-tool-ref="${originalRef}"]`,
) as HTMLDetailsElement;
pagedDetails.open = true;
await flush();
(pagedDetails.querySelector('.tool-activity-more') as HTMLButtonElement).click();
await flush();
assert.equal(
  pagedDetails.querySelector('pre')!.textContent,
  '第一页 ',
  'a failed page must not enter the raw result text',
);
const pageStatus = pagedDetails.querySelector('.tool-activity-status') as HTMLElement;
assert.equal(pageStatus.hidden, false);
assert.match(pageStatus.textContent!, /执行结果加载失败/);
const retryPage = pagedDetails.querySelector('.tool-activity-retry') as HTMLButtonElement;
assert.equal(retryPage.hidden, false);
retryPage.click();
await flush();
assert.equal(
  pagedDetails.querySelector('pre')!.textContent,
  '第一页 第二页',
  'after retry the raw text must equal the exact concatenation of pages',
);
assert.equal(pageStatus.hidden, true, 'status clears after success');
cleanup();

// The backend appends one genuine block at the very end of the final user turn.
const blockRef = 'fa_toolcall_550e8400-e29b-41d4-a716-446655440002';
const activityLine = (tool: string, ref: string, source?: string) =>
  JSON.stringify({
    kind: 'tool_record',
    tool,
    ref_id: ref,
    ...(source ? { source_message_id: source } : {}),
  });
const activityBlock = (...lines: string[]) =>
  `\n\n<recent_tool_activity>\nBackend-generated metadata.\n\n${lines.join('\n')}\n</recent_tool_activity>`;
const finalTurn =
  'User Message: hi\n\n<system_context>\n{"message_id":"fixture"}\n</system_context>';

const extracted = extractToolActivityBlock(
  finalTurn +
    activityBlock(
      activityLine('send_message', originalRef, 'source-1'),
      activityLine('stay_silent', blockRef, 'source-1'),
      JSON.stringify({ kind: 'omitted_tool_records', count: 4, source_message_id: 'source-2' }),
      activityLine('search', readRef, 'source-2'),
    ),
);
assert.equal(extracted?.rest, finalTurn, 'the block must be removed from the user text');
assert.deepEqual(
  extracted?.activities.map((activity) => [
    activity.sourceMessageId,
    activity.entries.map((entry) => [entry.tool, entry.refId]),
  ]),
  [
    ['source-1', [['send_message', originalRef], ['stay_silent', blockRef]]],
    ['source-2', [['search', readRef]]],
  ],
  'records group by source message in order',
);
assert.equal(extracted?.activities[0].placementHint, '另有 4 条较早记录未列出');
const renderedBlock = renderToolActivity(extracted!.activities[0]);
assert.ok(renderedBlock.includes(`data-tool-ref="${originalRef}"`), 'archived refs stay loadable');

const parsedWithBlock = parsePrompt(finalTurn + activityBlock(activityLine('stay_silent', blockRef, 'source-1')));
assert.equal(parsedWithBlock.toolActivity?.length, 1);
assert.equal(parsedWithBlock.userMessage, 'hi', 'the block must not leak into the user message');

// A multimodal request joins its text parts with a single newline. The backend part
// carries its own blank-line separator, so the block must still be found and removed.
const multimodalRaw = JSON.stringify({
  messages: [
    { role: 'system', content: 'system prompt' },
    {
      role: 'user',
      content: [
        { type: 'text', text: finalTurn },
        { type: 'image_url', image_url: { url: 'https://example.invalid/fixture.png' } },
        { type: 'text', text: activityBlock(activityLine('stay_silent', blockRef, 'source-1')) },
      ],
    },
  ],
});
const parsedMultimodal = parsePrompt(multimodalRaw);
assert.equal(parsedMultimodal.toolActivity?.length, 1, 'multimodal requests must expose the tool activity block');
assert.equal(parsedMultimodal.toolActivity?.[0].entries[0].refId, blockRef);
assert.equal(parsedMultimodal.userMessage, 'hi', 'the block must not stay in a multimodal user message');

// With tool calls in the request, the per-message view is shown. The block follows
// system_context, so it must be removed before the private envelope is parsed.
const privateTurn = (text: string) =>
  `User Message: ${text}\n\n<system_context>\n{"message_id":"fixture","user_id":"fixture-user"}\n</system_context>`;
const toolRequestRaw = JSON.stringify({
  messages: [
    { role: 'system', content: 'system prompt' },
    { role: 'user', content: privateTurn('earlier') },
    { role: 'assistant', content: '', tool_calls: [{ id: 'call-1', function: { name: 'stay_silent', arguments: '{}' } }] },
    { role: 'tool', tool_call_id: 'call-1', content: 'ok' },
    { role: 'user', content: privateTurn('hi') + activityBlock(activityLine('stay_silent', blockRef, 'source-1')) },
  ],
});
const parsedToolRequest = parsePrompt(toolRequestRaw);
const requestUsers = (parsedToolRequest.requestMessages ?? []).filter((message) => message.role === 'user');
assert.equal(requestUsers.length, 2, 'both user turns must be shown');
const finalRequestUser = requestUsers[requestUsers.length - 1];
assert.equal(finalRequestUser.content, 'hi', 'the private envelope must still be parsed');
assert.ok(
  finalRequestUser.promptContexts?.some((context) => context.name === 'system_context'),
  'system_context must still be split out',
);
assert.ok(
  !finalRequestUser.content.includes('recent_tool_activity') && !(finalRequestUser.rawText ?? '').includes('recent_tool_activity'),
  'the internal block must not stay in the request user message',
);
assert.equal(parsedToolRequest.toolActivity?.length, 1, 'the block is shown once, as its own card');
assert.equal(requestUsers[0].content, 'earlier', 'earlier user turns are untouched');

// The combination of both: text parts joined with a newline, plus tool calls in the request.
const multimodalToolRequestRaw = JSON.stringify({
  messages: [
    { role: 'system', content: 'system prompt' },
    { role: 'user', content: privateTurn('earlier') },
    { role: 'assistant', content: '', tool_calls: [{ id: 'call-1', function: { name: 'stay_silent', arguments: '{}' } }] },
    { role: 'tool', tool_call_id: 'call-1', content: 'ok' },
    {
      role: 'user',
      content: [
        { type: 'text', text: privateTurn('hi') },
        { type: 'image_url', image_url: { url: 'https://example.invalid/fixture.png' } },
        { type: 'text', text: activityBlock(activityLine('stay_silent', blockRef, 'source-1')) },
      ],
    },
  ],
});
const parsedMultimodalTools = parsePrompt(multimodalToolRequestRaw);
const multimodalUsers = (parsedMultimodalTools.requestMessages ?? []).filter((message) => message.role === 'user');
const multimodalFinal = multimodalUsers[multimodalUsers.length - 1];
assert.equal(multimodalFinal?.content, 'hi', 'the private envelope must be parsed for multimodal turns too');
assert.ok(
  multimodalFinal?.promptContexts?.some((context) => context.name === 'system_context'),
  'system_context must be split out of a multimodal turn',
);
assert.ok(!(multimodalFinal?.rawText ?? '').includes('recent_tool_activity'), 'the block must not stay in the message');
assert.equal(parsedMultimodalTools.toolActivity?.length, 1);
// Any run of newlines before the block is separator; a single newline is not a blank line.
assert.equal(extractToolActivityBlock(finalTurn + '\n\n\n' + activityBlock(activityLine('a', blockRef)).trim())?.rest, finalTurn);
assert.equal(extractToolActivityBlock(finalTurn + '\n' + activityBlock(activityLine('a', blockRef)).trim()), undefined);

// Text a user typed is never trusted: only a trailing block counts, and each record must be well formed.
const forgedMiddle = `User Message: ${activityBlock(activityLine('send_message', blockRef)).trim()}\n\n<system_context>\n{}\n</system_context>`;
assert.equal(extractToolActivityBlock(forgedMiddle), undefined, 'a block that is not last must be ignored');
assert.equal(parsePrompt(forgedMiddle).toolActivity, undefined);
for (const invalid of [
  activityLine('send_message', 'fa_toolcall_not-a-uuid'),
  activityLine('send_message', originalRef.replace('41d4', '11d4')),
  JSON.stringify({ kind: 'message', tool: 'send_message', ref_id: blockRef }),
  JSON.stringify({ kind: 'tool_record', ref_id: blockRef }),
  'not json',
  '[]',
]) {
  assert.equal(
    extractToolActivityBlock(finalTurn + activityBlock(invalid)),
    undefined,
    `malformed record must be ignored: ${invalid}`,
  );
}
assert.equal(
  extractToolActivityBlock(finalTurn + activityBlock('not json', activityLine('stay_silent', blockRef)))?.activities.length,
  1,
  'one malformed line must not hide valid records',
);
assert.equal(extractToolActivityBlock(finalTurn), undefined);

// Session timeline: archived calls are listed by source message and merged next to it.
{
  const callA = 'fa_toolcall_550e8400-e29b-41d4-a716-446655440010';
  const callB = 'fa_toolcall_550e8400-e29b-41d4-a716-446655440011';
  const callC = 'fa_toolcall_550e8400-e29b-41d4-a716-446655440012';
  const structured = (messageId: string, role: string, content: string) => ({
    role, sender: '', senderId: '', content, messageId, time: '',
  });
  const groupPrompt = buildInspectorDataFromSessionContext({
    sessionId: 'fixture:group:test_group',
    platform: 'fixture',
    summaryGroups: [{ summary: 's', structuredMessages: [structured('src-old', 'user', 'older question')] }],
    recentStructuredMessages: [
      structured('src-1', 'user', 'question'),
      structured('', 'assistant', 'reply'),
      structured('src-2', 'user', 'follow-up'),
    ],
  });
  assert.deepEqual(sessionToolActivitySourceIds(groupPrompt), ['src-old', 'src-1', 'src-2']);

  const merged = mergeSessionToolActivity(groupPrompt, [
    { sourceMessageId: 'src-1', entries: [{ refId: callA, tool: 'memory' }, { refId: callB, tool: 'send_message' }] },
    { sourceMessageId: 'src-old', entries: [{ refId: callC, tool: 'stay_silent' }], total: 14 },
    { sourceMessageId: 'src-not-shown', entries: [{ refId: 'fa_toolcall_550e8400-e29b-41d4-a716-446655440013', tool: 'ghost' }] },
  ]);
  const recent = merged.recentMessages;
  assert.equal(recent.length, 4, 'one card for src-1 only');
  assert.equal(recent[0].id, 'src-1');
  assert.deepEqual(
    recent[1].toolActivity?.entries.map((entry) => [entry.tool, entry.refId]),
    [['memory', callA], ['send_message', callB]],
  );
  assert.equal(recent[1].toolActivity?.sourceMessageId, 'src-1');
  assert.equal(recent[1].role, 'assistant');
  assert.equal(recent[2].content, 'reply', 'the delivered reply stays after the card');
  const older = merged.summaryGroups[0].parsedMessages!;
  assert.equal(older.length, 2, 'cards are also placed inside compacted batches');
  assert.match(older[1].toolActivity?.placementHint ?? '', /另有 13 条较早记录未列出/);
  assert.equal(groupPrompt.recentMessages.length, 3, 'the input prompt must not be mutated');
  assert.equal(mergeSessionToolActivity(groupPrompt, []), groupPrompt);

  // Calls already shown by pre-upgrade history notes are not repeated.
  const withLegacy = buildInspectorDataFromSessionContext({
    sessionId: 'fixture:group:test_group',
    platform: 'fixture',
    recentStructuredMessages: [structured('src-1', 'user', 'question'), structured('', 'assistant', current)],
    history: [],
  });
  const deduped = mergeSessionToolActivity(withLegacy, [
    { sourceMessageId: 'src-1', entries: [{ refId: originalRef, tool: 'send_message' }, { refId: callA, tool: 'memory' }] },
  ]);
  const cards = deduped.recentMessages.filter((message) => message.toolActivity);
  assert.equal(cards.length, 1);
  assert.deepEqual(
    cards[0].toolActivity?.entries.map((entry) => entry.refId),
    [callA],
    'a call already on the timeline was duplicated',
  );
  assert.ok(deduped.recentMessages.some((message) => message.content === current), 'the pre-upgrade note must stay');

  // When every listed call is already shown, nothing is inserted.
  const allShown = mergeSessionToolActivity(withLegacy, [
    { sourceMessageId: 'src-1', entries: [{ refId: originalRef, tool: 'send_message' }] },
  ]);
  assert.equal(allShown.recentMessages.filter((message) => message.toolActivity).length, 0);

  // Private turns only carry the platform message ID inside their system_context envelope.
  const envelope = (id: string) =>
    `User Message: hello\n\n<system_context>\n${JSON.stringify({ message_id: id, user_id: 'fixture-user' })}\n</system_context>`;
  const privatePrompt = buildInspectorDataFromSessionContext({
    sessionId: 'fixture:private:test_user',
    platform: 'fixture',
    recentStructuredMessages: [structured('', 'user', envelope('private-src')), structured('', 'assistant', 'hi')],
  });
  assert.deepEqual(sessionToolActivitySourceIds(privatePrompt), ['private-src']);
  const privateMerged = mergeSessionToolActivity(privatePrompt, [
    { sourceMessageId: 'private-src', entries: [{ refId: callA, tool: 'memory' }] },
  ]);
  assert.equal(privateMerged.recentMessages.length, 3);
  assert.equal(privateMerged.recentMessages[1].toolActivity?.entries[0].tool, 'memory');
  assert.equal(privateMerged.recentMessages[2].content, 'hi');

  // A malformed envelope simply has no source and gets no card.
  const malformed = buildInspectorDataFromSessionContext({
    sessionId: 'fixture:private:test_user',
    platform: 'fixture',
    recentStructuredMessages: [structured('', 'user', 'User Message: x\n\n<system_context>\n{broken\n</system_context>')],
  });
  assert.deepEqual(sessionToolActivitySourceIds(malformed), []);

  container.innerHTML = '';
  renderPromptInspector(container, merged, {});
  assert.match(container.innerHTML, /tool-activity/);
  assert.match(container.innerHTML, new RegExp(callA));
  assert.match(container.innerHTML, /send_message/);
}

// Restricted and interrupted records remain inspectable in the admin panel with clear model-shielded notices.
{
  const mcpArgs = '{"url":"https://docs.volcengine.com/docs/ark/latest-model?lang=zh"}';
  const mcpSummary = '[FrostAgent 错误] MCP 工具执行失败 (Ref_ID: 8b94e642-f4ba-4ea2-a58a-3447f4e956c6)';
  const mcpEnvelope = '{"content":[{"type":"text","text":"extract_failed\\nUnable to extract content from the URL."}],"isError":true}';
  cleanup = renderPromptInspector(container, data, {
    loadToolArguments: async (refId, options) => {
      if (!options?.includeResult) {
        return { refId, tool: 'send_message', argumentsRaw: mcpArgs, argumentsAvailable: true, restricted: true, state: 'complete' };
      }
      return {
        refId,
        tool: 'send_message',
        argumentsRaw: '',
        argumentsAvailable: false,
        resultRaw: mcpSummary,
        resultAvailable: true,
        restricted: true,
        state: 'complete',
        mcpResultRaw: mcpEnvelope,
        modelResultRaw: `${mcpSummary}\n(ref_id: ${refId})`,
      };
    },
  });

  const restrictedArgs = container.querySelector(
    `details[data-tool-ref="${originalRef}"]:not([data-tool-field])`,
  ) as HTMLDetailsElement;
  restrictedArgs.open = true;
  await flush();
  assert.equal(restrictedArgs.querySelector('pre')!.textContent, mcpArgs);
  assert.match(restrictedArgs.querySelector('.tool-activity-notice')!.textContent!, /已对模型屏蔽/);

  const restrictedRes = container.querySelector(
    `details[data-tool-field="result"][data-tool-ref="${originalRef}"]`,
  ) as HTMLDetailsElement;
  restrictedRes.open = true;
  await flush();
  assert.equal(restrictedRes.querySelector('pre')!.textContent, mcpSummary);
  assert.match(restrictedRes.querySelector('.tool-activity-notice')!.textContent!, /已对模型屏蔽/);
  assert.match(restrictedRes.querySelector('.tool-activity-diagnostics')!.textContent!, /extract_failed/);
  assert.doesNotMatch(
    restrictedRes.querySelector('.tool-activity-diagnostics')!.textContent!,
    /模型实际接收/,
    'identical ref-suffixed model summary should not duplicate resultRaw',
  );
  cleanup();

  cleanup = renderPromptInspector(container, data, {
    loadToolArguments: async (refId, options) => ({
      refId,
      tool: 'send_message',
      argumentsRaw: options?.includeResult ? '' : '<script>arg</script>',
      argumentsAvailable: !options?.includeResult,
      resultRaw: '',
      resultAvailable: Boolean(options?.includeResult),
      restricted: true,
      state: 'complete',
      errorRaw: options?.includeResult ? '<img src=x onerror=alert(1)>exec failed' : '',
      modelResultRaw: options?.includeResult ? '[FrostAgent 错误] 工具执行失败 (Ref_ID: fixture-go-err)' : '',
    }),
  });
  const goRes = container.querySelector(
    `details[data-tool-field="result"][data-tool-ref="${originalRef}"]`,
  ) as HTMLDetailsElement;
  goRes.open = true;
  await flush();
  assert.match(goRes.querySelector('pre')!.textContent!, /详见下方原始详情/);
  assert.match(goRes.querySelector('.tool-activity-diagnostics')!.textContent!, /exec failed/);
  assert.match(goRes.querySelector('.tool-activity-diagnostics')!.textContent!, /模型实际接收/);
  assert.equal(goRes.querySelector('img'), null, 'diagnostics must be text-escaped');
  cleanup();

  cleanup = renderPromptInspector(container, data, {
    loadToolArguments: async (refId, options) => {
      if (!options?.includeResult) {
        return { refId, tool: 'send_message', argumentsRaw: queryArguments, argumentsAvailable: true, restricted: true, state: 'complete' };
      }
      const offset = options.resultOffset ?? 0;
      return {
        refId,
        tool: 'send_message',
        argumentsRaw: '',
        argumentsAvailable: false,
        resultRaw: offset === 0 ? '第一页原文 ' : '第二页原文',
        resultAvailable: true,
        resultHasMore: offset === 0,
        resultNextOffset: offset === 0 ? 1 : 0,
        restricted: true,
        state: 'complete',
        modelResultRaw: offset === 0 ? `安全审查拦截：外部工具结果已隔离\n(ref_id: ${refId})` : '',
      };
    },
  });
  const watchdogRes = container.querySelector(
    `details[data-tool-field="result"][data-tool-ref="${originalRef}"]`,
  ) as HTMLDetailsElement;
  watchdogRes.open = true;
  await flush();
  assert.equal(watchdogRes.querySelector('pre')!.textContent, '第一页原文 ');
  assert.match(watchdogRes.querySelector('.tool-activity-diagnostics')!.textContent!, /安全审查拦截/);
  (watchdogRes.querySelector('.tool-activity-more') as HTMLButtonElement).click();
  await flush();
  assert.equal(watchdogRes.querySelector('pre')!.textContent, '第一页原文 第二页原文');
  assert.match(watchdogRes.querySelector('.tool-activity-diagnostics')!.textContent!, /安全审查拦截/);
  cleanup();

  let execPhase: 'executed' | 'complete-pass' | 'complete-blocked' = 'executed';
  let execLoads = 0;
  cleanup = renderPromptInspector(container, data, {
    loadToolArguments: async (refId, options) => {
      execLoads++;
      if (!options?.includeResult) {
        return {
          refId,
          tool: 'send_message',
          argumentsRaw: queryArguments,
          argumentsAvailable: true,
          restricted: execPhase !== 'complete-pass',
          state: execPhase === 'executed' ? 'executed' : 'complete',
        };
      }
      const offset = options.resultOffset ?? 0;
      const pages = ['第一页执行原文 ', '第二页执行原文'];
      return {
        refId,
        tool: 'send_message',
        argumentsRaw: '',
        argumentsAvailable: false,
        resultRaw: pages[offset] ?? '',
        resultAvailable: true,
        resultHasMore: offset === 0,
        resultNextOffset: offset === 0 ? 1 : 0,
        restricted: execPhase !== 'complete-pass',
        state: execPhase === 'executed' ? 'executed' : 'complete',
        modelResultRaw:
          execPhase === 'complete-blocked' && offset === 0
            ? `安全审查拦截：外部工具结果已隔离\n(ref_id: ${refId})`
            : '',
      };
    },
  });

  // Permanently executed (interrupted) records stay viewable, do not falsely claim interruption, and allow refresh without duplicating pages.
  const execArgs = container.querySelector(
    `details[data-tool-ref="${originalRef}"]:not([data-tool-field])`,
  ) as HTMLDetailsElement;
  execArgs.open = true;
  await flush();
  assert.equal(execArgs.querySelector('pre')!.textContent, queryArguments);
  assert.equal(execArgs.dataset.argumentsState, 'executed');
  assert.match(execArgs.querySelector('.tool-activity-notice')!.textContent!, /等待安全审查或最终结果确认/);
  assert.doesNotMatch(execArgs.querySelector('.tool-activity-notice')!.textContent!, /调用在完成前被中断/);
  const argsRefresh = execArgs.querySelector('.tool-activity-refresh') as HTMLButtonElement;
  assert.equal(argsRefresh.hidden, false);

  const execRes = container.querySelector(
    `details[data-tool-field="result"][data-tool-ref="${originalRef}"]`,
  ) as HTMLDetailsElement;
  execRes.open = true;
  await flush();
  assert.equal(execRes.querySelector('pre')!.textContent, '第一页执行原文 ');
  assert.equal(execRes.dataset.resultState, 'executed');
  const resRefresh = execRes.querySelector('.tool-activity-refresh') as HTMLButtonElement;
  assert.equal(resRefresh.hidden, false);
  // Paging while executed loads page 2 without duplicating page 1; reopening refreshes cleanly.
  (execRes.querySelector('.tool-activity-more') as HTMLButtonElement).click();
  await flush();
  assert.equal(execRes.querySelector('pre')!.textContent, '第一页执行原文 第二页执行原文');
  assert.equal(resRefresh.hidden, false);
  execRes.open = false;
  await flush();
  execRes.open = true;
  await flush();
  assert.equal(execRes.querySelector('pre')!.textContent, '第一页执行原文 ');
  assert.equal(resRefresh.hidden, false);

  // Transition executed -> complete (normal pass): refresh clears provisional notice and loads pages cleanly without duplicates.
  execPhase = 'complete-pass';
  argsRefresh.click();
  await flush();
  assert.equal(execArgs.dataset.argumentsState, 'loaded');
  assert.equal(execArgs.querySelector<HTMLElement>('.tool-activity-notice')!.hidden, true);
  assert.equal(argsRefresh.hidden, true);
  resRefresh.click();
  await flush();
  assert.equal(execRes.dataset.resultState, 'loaded');
  assert.equal(execRes.querySelector<HTMLElement>('.tool-activity-notice')!.hidden, true);
  assert.equal(execRes.querySelector<HTMLElement>('.tool-activity-diagnostics')!.hidden, true);
  assert.equal(resRefresh.hidden, true);
  assert.equal(execRes.querySelector('pre')!.textContent, '第一页执行原文 ');
  (execRes.querySelector('.tool-activity-more') as HTMLButtonElement).click();
  await flush();
  assert.equal(execRes.querySelector('pre')!.textContent, '第一页执行原文 第二页执行原文');
  // Reopening a completed record stays cached.
  const loadsBeforeReopen = execLoads;
  execRes.open = false;
  await flush();
  execRes.open = true;
  await flush();
  assert.equal(execLoads, loadsBeforeReopen);
  cleanup();

  // Transition executed -> complete (watchdog blocked): notice and diagnostics update on re-open/refresh, and page 2 never duplicates.
  execPhase = 'executed';
  cleanup = renderPromptInspector(container, data, {
    loadToolArguments: async (refId, options) => {
      if (!options?.includeResult) {
        return {
          refId,
          tool: 'send_message',
          argumentsRaw: queryArguments,
          argumentsAvailable: true,
          restricted: true,
          state: execPhase === 'executed' ? 'executed' : 'complete',
        };
      }
      const offset = options.resultOffset ?? 0;
      const pages = ['拦截第一页 ', '拦截第二页'];
      return {
        refId,
        tool: 'send_message',
        argumentsRaw: '',
        argumentsAvailable: false,
        resultRaw: pages[offset] ?? '',
        resultAvailable: true,
        resultHasMore: offset === 0,
        resultNextOffset: offset === 0 ? 1 : 0,
        restricted: true,
        state: execPhase === 'executed' ? 'executed' : 'complete',
        modelResultRaw:
          execPhase === 'complete-blocked' && offset === 0
            ? `安全审查拦截：外部工具结果已隔离\n(ref_id: ${refId})`
            : '',
      };
    },
  });
  const transitionRes = container.querySelector(
    `details[data-tool-field="result"][data-tool-ref="${originalRef}"]`,
  ) as HTMLDetailsElement;
  transitionRes.open = true;
  await flush();
  assert.match(transitionRes.querySelector('.tool-activity-notice')!.textContent!, /等待安全审查/);
  assert.equal(transitionRes.querySelector<HTMLElement>('.tool-activity-diagnostics')!.hidden, true);
  execPhase = 'complete-blocked';
  // Even if offset > 0 observes complete first, refresh remains available until offset 0 updates notice & diagnostics.
  (transitionRes.querySelector('.tool-activity-more') as HTMLButtonElement).click();
  await flush();
  assert.equal(transitionRes.querySelector('pre')!.textContent, '拦截第一页 拦截第二页');
  assert.equal(transitionRes.dataset.resultState, 'executed');
  const transitionRefresh = transitionRes.querySelector('.tool-activity-refresh') as HTMLButtonElement;
  assert.equal(transitionRefresh.hidden, false);
  transitionRefresh.click();
  await flush();
  assert.equal(transitionRes.dataset.resultState, 'loaded');
  assert.equal(transitionRefresh.hidden, true);
  assert.match(transitionRes.querySelector('.tool-activity-notice')!.textContent!, /模型调用 read_tool_history 无法读取/);
  assert.match(transitionRes.querySelector('.tool-activity-diagnostics')!.textContent!, /安全审查拦截/);
  assert.equal(transitionRes.querySelector('pre')!.textContent, '拦截第一页 ');
  (transitionRes.querySelector('.tool-activity-more') as HTMLButtonElement).click();
  await flush();
  assert.equal(transitionRes.querySelector('pre')!.textContent, '拦截第一页 拦截第二页');
  cleanup();
}

dom.window.close();
process.stdout.write(
  'PASS: distinct tool calls, historical reads, legacy records, native results, folding, escaping, trailing activity blocks and session timeline merging\n',
);
