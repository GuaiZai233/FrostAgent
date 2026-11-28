import { escapeHtml } from '../utils/formatters';
import { icon } from './icons';

export interface ToolActivityEntry {
  tool: string;
  refId?: string;
  readTargetRefId?: string;
  callId?: string;
  arguments?: string;
  result?: string;
  phase?: 'call' | 'result';
}

export interface ToolActivity {
  entries: ToolActivityEntry[];
  sourceMessageId?: string;
  placementHint?: string;
  raw?: string;
}

export interface ToolLoadOptions {
  includeResult?: boolean;
  resultOffset?: number;
}

export type ToolArgumentsLoader = (
  refId: string,
  options?: ToolLoadOptions,
) => Promise<{
  refId: string;
  tool: string;
  argumentsRaw: string;
  argumentsAvailable: boolean;
  argumentsPending?: boolean;
  resultRaw?: string;
  resultAvailable?: boolean;
  resultHasMore?: boolean;
  resultNextOffset?: number;
  restricted?: boolean;
  state?: string;
  errorRaw?: string;
  mcpResultRaw?: string;
  modelResultRaw?: string;
}>;

const refPattern =
  'fa_toolcall_[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}';
const entryPattern = new RegExp(
  `^(?:- (工具调用|历史回查): )?([^\\s()]+) \\(ref_id: (${refPattern})\\)(?: \\(read_target_ref_id: (${refPattern})\\))?$`,
);
const headerPattern =
  /^【(?:工具调用记录；原文可用 read_tool_history 回查|工具执行记录)】(?:[ \t]+来源消息: ("(?:\\.|[^"\\])*"))?$/;

// Trailing line the backend appends to every injected record; older records lack it.
const noticeLine =
  '此为内部占位元数据，由后端自动生成，非常规Assistant输出结果，禁止模仿该格式输出！';

// Legacy notes have no API metadata. Recognize only the complete backend format;
// ordinary messages mentioning a tool or a reference remain ordinary messages.
export function parseToolHistoryNote(
  content: string,
): ToolActivity | undefined {
  const lines = content.trim().split(/\r?\n/);
  if (lines.length > 1 && lines[lines.length - 1] === noticeLine) lines.pop();
  const header = lines.shift()?.match(headerPattern);
  if (!header) return undefined;
  let source = header[1];
  if (lines[0]?.startsWith('来源消息: '))
    source = lines.shift()!.slice('来源消息: '.length);
  let sourceMessageId: string | undefined;
  if (source) {
    try {
      const value: unknown = JSON.parse(source);
      if (typeof value !== 'string') return undefined;
      sourceMessageId = value;
    } catch {
      return undefined;
    }
  }
  const entries: ToolActivityEntry[] = [];
  for (const line of lines) {
    const match = line.match(entryPattern);
    if (
      !match ||
      (match[1] &&
        (match[1] === '历史回查') !== (match[2] === 'read_tool_history'))
    )
      return undefined;
    entries.push({
      tool: match[2],
      refId: match[3],
      readTargetRefId: match[4],
    });
  }
  return entries.length
    ? { entries, sourceMessageId, raw: content }
    : undefined;
}

// The backend neutralizes every lookalike tag, then appends one genuine block at
// the very end of the final user turn. Only that trailing block is trusted, so
// text a user typed earlier in a message can never render as tool activity.
// The block is preceded by a blank line; a multimodal request adds the newline
// that joins its text parts, so the whole run of newlines belongs to the separator.
const toolActivityBlockPattern =
  /\n{2,}<recent_tool_activity>\n([\s\S]*?)\n<\/recent_tool_activity>\s*$/;
const refMatcher = new RegExp(`^${refPattern}$`);

export function extractToolActivityBlock(
  content: string,
): { rest: string; activities: ToolActivity[] } | undefined {
  const match = content.match(toolActivityBlockPattern);
  if (!match || match.index === undefined) return undefined;
  const activities: ToolActivity[] = [];
  let omitted = 0;
  for (const line of match[1].split(/\r?\n/)) {
    if (!line.startsWith('{')) continue;
    let value: unknown;
    try {
      value = JSON.parse(line);
    } catch {
      continue;
    }
    if (!value || typeof value !== 'object') continue;
    const record = value as Record<string, unknown>;
    if (record.kind === 'omitted_tool_records') {
      if (typeof record.count === 'number') omitted += record.count;
      continue;
    }
    if (
      record.kind !== 'tool_record' ||
      typeof record.tool !== 'string' ||
      typeof record.ref_id !== 'string' ||
      !refMatcher.test(record.ref_id)
    )
      continue;
    const sourceMessageId =
      typeof record.source_message_id === 'string' && record.source_message_id
        ? record.source_message_id
        : undefined;
    const entry: ToolActivityEntry = { tool: record.tool, refId: record.ref_id };
    const last = activities[activities.length - 1];
    if (last && last.sourceMessageId === sourceMessageId)
      last.entries.push(entry);
    else activities.push({ entries: [entry], sourceMessageId });
  }
  if (!activities.length) return undefined;
  if (omitted)
    activities[0].placementHint = `另有 ${omitted} 条较早记录未列出`;
  return { rest: content.slice(0, match.index), activities };
}

export function readTargetFromArguments(
  tool: string,
  args: string,
): string | undefined {
  if (tool !== 'read_tool_history') return undefined;
  try {
    const parsed: unknown = JSON.parse(args);
    if (
      parsed &&
      typeof parsed === 'object' &&
      'read_target_ref_id' in parsed &&
      typeof parsed.read_target_ref_id === 'string'
    )
      return parsed.read_target_ref_id;
  } catch {
    /* Invalid arguments are shown as supplied. */
  }
  return undefined;
}

export function refFromToolResult(content: string): string | undefined {
  return content.match(new RegExp(`\\(ref_id: (${refPattern})\\)\\s*$`))?.[1];
}

export function renderToolActivity(activity: ToolActivity): string {
  const reads = activity.entries.filter(
    (entry) => entry.tool === 'read_tool_history',
  ).length;
  const results = activity.entries.filter(
    (entry) => entry.phase === 'result',
  ).length;
  const calls = activity.entries.length - reads;
  const counts = [
    calls
      ? `${calls} 次工具${results === activity.entries.length ? '返回' : '调用'}`
      : '',
    reads
      ? `${reads} 次历史回查${results === activity.entries.length ? '返回' : ''}`
      : '',
  ]
    .filter(Boolean)
    .join(' · ');
  const names = new Map<string, number>();
  for (const entry of activity.entries)
    names.set(entry.tool, (names.get(entry.tool) || 0) + 1);
  return `<details class="tool-activity" data-tool-activity>
    <summary class="tool-activity-summary">
      <span class="tool-activity-heading">${icon('wrench', 'w-3.5 h-3.5')}<span>工具执行记录</span></span>
      <span class="tool-activity-count">${escapeHtml(counts || '内部记录')}</span>
      ${reads ? '<span class="tool-activity-read-badge">历史回查</span>' : ''}
      <span class="tool-activity-chevron">${icon('chevron_down', 'w-3.5 h-3.5')}</span>
      <span class="tool-activity-preview">${[...names].map(([name, count]) => escapeHtml(name) + (count > 1 ? ` ×${count}` : '')).join(' · ')}${activity.placementHint ? ` · ${escapeHtml(activity.placementHint)}` : ''}</span>
    </summary>
    <div class="tool-activity-body">
      ${activity.sourceMessageId ? `<div class="tool-activity-source">来源消息 <code>${escapeHtml(activity.sourceMessageId)}</code></div>` : ''}
      ${activity.entries
        .map((entry, index) => {
          const read = entry.tool === 'read_tool_history';
          const label =
            entry.phase === 'result'
              ? read
                ? '回查返回'
                : '工具返回'
              : read
                ? '历史回查'
                : '工具调用';
          const id = entry.refId || entry.callId;
          return `<div class="tool-activity-entry" data-kind="${read ? 'history-read' : 'tool-call'}">
          <div class="tool-activity-entry-title"><span class="tool-activity-order">${index + 1}</span><span class="tool-activity-kind">${label}</span><code>${escapeHtml(entry.tool)}</code>${id ? `<code class="tool-activity-ref">(${escapeHtml(id)})</code>` : ''}</div>
          ${
            entry.phase === 'result'
              ? `<details class="tool-activity-details"><summary>返回原文</summary><pre>${escapeHtml(entry.result || '')}</pre></details>`
              : `
          <details class="tool-activity-details" ${entry.refId ? `data-tool-ref="${escapeHtml(entry.refId)}" data-tool-name="${escapeHtml(entry.tool)}"` : ''} data-arguments-state="${entry.arguments !== undefined ? 'loaded' : 'idle'}">
            <summary>详细参数</summary>
            <p class="tool-activity-notice" hidden></p>
            <pre class="tool-activity-arguments">${escapeHtml(entry.arguments ?? '展开后加载参数')}</pre>
            <button type="button" class="btn btn-ghost btn-sm tool-activity-retry" hidden>重试</button>
          </details>
          ${entry.refId ? `<details class="tool-activity-details" data-tool-ref="${escapeHtml(entry.refId)}" data-tool-name="${escapeHtml(entry.tool)}" data-tool-field="result" data-result-state="idle">
            <summary>工具执行结果</summary>
            <p class="tool-activity-notice" hidden></p>
            <pre class="tool-activity-result">展开后加载结果</pre>
            <div class="tool-activity-diagnostics" hidden></div>
            <p class="tool-activity-status" hidden></p>
            <button type="button" class="btn btn-ghost btn-sm tool-activity-more" hidden>加载更多</button>
            <button type="button" class="btn btn-ghost btn-sm tool-activity-retry" hidden>重试</button>
          </details>` : ''}`
          }
        </div>`;
        })
        .join('')}
      ${!activity.entries.length && activity.raw ? `<pre class="tool-activity-raw">${escapeHtml(activity.raw)}</pre>` : ''}
    </div>
  </details>`;
}

type ToolHistoryEntryPayload = Awaited<ReturnType<ToolArgumentsLoader>>;

function formatToolNotice(result: ToolHistoryEntryPayload): string {
  if (result.state === 'executed') {
    return '已对模型屏蔽：调用在完成前被中断，以下为后台执行记录（仅管理面板可见）';
  }
  if (result.restricted) {
    return '已对模型屏蔽：模型调用 read_tool_history 无法读取下列原始详情（仅管理面板可见）';
  }
  return '';
}

function diagnosticItems(
  refId: string,
  result: ToolHistoryEntryPayload,
): Array<[string, string]> {
  const items: Array<[string, string]> = [];
  if (result.errorRaw) items.push(['执行错误', result.errorRaw]);
  if (result.mcpResultRaw) items.push(['MCP 原始返回', result.mcpResultRaw]);
  if (
    result.modelResultRaw &&
    result.modelResultRaw !== result.resultRaw &&
    result.modelResultRaw !== `${result.resultRaw ?? ''}\n(ref_id: ${refId})`
  ) {
    items.push(['模型实际接收', result.modelResultRaw]);
  }
  return items;
}

export function bindToolArguments(
  container: HTMLElement,
  load?: ToolArgumentsLoader,
): () => void {
  let active = true;
  const requests = new Map<string, ReturnType<ToolArgumentsLoader>>();
  for (const details of container.querySelectorAll?.<HTMLDetailsElement>(
    'details[data-tool-ref]',
  ) || []) {
    const pre = details.querySelector<HTMLElement>('pre')!;
    const retry = details.querySelector<HTMLButtonElement>(
      '.tool-activity-retry',
    )!;
    const refId = details.dataset.toolRef!;
    // Arguments and results share one loader request per reference.
    const isResult = details.dataset.toolField === 'result';
    const stateKey = isResult ? 'resultState' : 'argumentsState';
    const copy = isResult
      ? {
          loading: '正在加载执行结果…',
          unsupported: '当前视图无法加载执行结果',
          unavailable: '执行结果已隔离，无法展示',
          failed: '执行结果加载失败，请重试',
        }
      : {
          loading: '正在加载参数…',
          unsupported: '当前视图无法加载历史参数',
          unavailable: '参数已隔离，无法展示',
          failed: '参数加载失败，请重试',
        };
    const more = details.querySelector<HTMLButtonElement>(
      '.tool-activity-more',
    );
    const notice = details.querySelector<HTMLElement>('.tool-activity-notice');
    const diagnostics = details.querySelector<HTMLElement>(
      '.tool-activity-diagnostics',
    );
    // Status text is kept out of <pre>, which holds only the raw result.
    const status = details.querySelector<HTMLElement>('.tool-activity-status');
    const setStatus = (message: string) => {
      if (!status) return;
      status.textContent = message;
      status.hidden = !message;
    };
    const setNotice = (message: string) => {
      if (!notice) return;
      notice.textContent = message;
      notice.hidden = !message;
    };
    const setDiagnostics = (items: Array<[string, string]>) => {
      if (!diagnostics) return false;
      diagnostics.replaceChildren();
      if (!items.length) {
        diagnostics.hidden = true;
        return false;
      }
      const doc = details.ownerDocument;
      for (const [label, value] of items) {
        const item = doc.createElement('div');
        item.className = 'tool-activity-diagnostic-item';
        const heading = doc.createElement('div');
        heading.className = 'tool-activity-diagnostic-label';
        heading.textContent = label;
        const body = doc.createElement('div');
        body.className = 'tool-activity-diagnostic-body';
        body.textContent = value;
        item.append(heading, body);
        diagnostics.append(item);
      }
      diagnostics.hidden = false;
      return true;
    };
    // Each (reference, mode, page) is requested once; arguments never carry
    // the result, and result pages are fetched only when they are asked for.
    const fetchPage = (options: ToolLoadOptions) => {
      const key = `${refId}\u0000${options.includeResult ? 1 : 0}\u0000${options.resultOffset ?? 0}`;
      let request = requests.get(key);
      if (!request) {
        request = load!(refId, options);
        requests.set(key, request);
      }
      return { key, request };
    };
    const fetchArguments = async (offset = 0) => {
      if (!active || !details.isConnected) return;
      const state = details.dataset[stateKey];
      if (state === 'loading' || (offset === 0 && state === 'loaded')) return;
      if (!load) {
        pre.textContent = copy.unsupported;
        return;
      }
      details.dataset[stateKey] = 'loading';
      details.dataset.requestOffset = String(offset);
      if (offset === 0) {
        pre.textContent = copy.loading;
        setNotice('');
        setDiagnostics([]);
      }
      setStatus('');
      retry.hidden = true;
      if (more) more.hidden = true;
      const options: ToolLoadOptions = isResult
        ? { includeResult: true, resultOffset: offset }
        : { includeResult: false };
      try {
        const { key, request } = fetchPage(options);
        const result = await request;
        if (!active || !details.isConnected) return;
        if (result.refId !== refId || result.tool !== details.dataset.toolName)
          throw new Error('工具记录不匹配');
        if (result.argumentsPending) {
          requests.delete(key);
          pre.textContent = '调用正在执行或审查中，请稍后重试';
          details.dataset[stateKey] = 'pending';
          retry.hidden = false;
          return;
        }
        if (isResult) {
          if (!result.resultAvailable) {
            pre.textContent = copy.unavailable;
          } else if (offset === 0) {
            setNotice(formatToolNotice(result));
            const hasDiagnostics = setDiagnostics(diagnosticItems(refId, result));
            pre.textContent =
              result.resultRaw ||
              (hasDiagnostics ? '（无普通输出，详见下方原始详情）' : '（无返回内容）');
          } else {
            pre.textContent += result.resultRaw || '';
          }
          if (more) {
            more.hidden = !result.resultHasMore;
            details.dataset.resultNextOffset = String(
              result.resultNextOffset ?? 0,
            );
          }
        } else {
          if (!result.argumentsAvailable) {
            pre.textContent = copy.unavailable;
          } else {
            setNotice(formatToolNotice(result));
            pre.textContent = result.argumentsRaw;
          }
        }
        details.dataset[stateKey] = 'loaded';
      } catch {
        requests.delete(`${refId}\u0000${isResult ? 1 : 0}\u0000${offset}`);
        if (!active || !details.isConnected) return;
        details.dataset[stateKey] = 'error';
        // Pages already shown stay intact; a failed later page is reported in
        // the status line, so a successful retry yields exactly the raw text.
        if (offset === 0) {
          setNotice('');
          setDiagnostics([]);
          pre.textContent = copy.failed;
        } else {
          setStatus(copy.failed);
        }
        retry.hidden = false;
      }
    };
    details.addEventListener('toggle', () => {
      if (details.open) void fetchArguments(0);
    });
    retry.addEventListener('click', () => {
      void fetchArguments(Number(details.dataset.requestOffset || 0));
    });
    more?.addEventListener('click', () => {
      void fetchArguments(Number(details.dataset.resultNextOffset || 0));
    });
  }
  return () => {
    active = false;
  };
}
