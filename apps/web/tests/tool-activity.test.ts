import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import {
  buildInspectorDataFromSessionContext,
  parsePrompt,
  renderPromptInspector,
} from '../src/components/prompt-inspector';
import {
  parseToolHistoryNote,
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
for (const content of [
  'I can use read_tool_history',
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
dom.window.close();
process.stdout.write(
  'PASS: distinct tool calls, historical reads, legacy records, native results, folding and escaping\n',
);
