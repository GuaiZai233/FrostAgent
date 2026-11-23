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
}>;

const refPattern =
  'fa_toolcall_[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}';
const entryPattern = new RegExp(
  `^(?:- (工具调用|历史回查): )?([^\\s()]+) \\(ref_id: (${refPattern})\\)(?: \\(read_target_ref_id: (${refPattern})\\))?$`,
);
const headerPattern =
  /^【(?:工具调用记录；原文可用 read_tool_history 回查|工具执行记录)】(?:[ \t]+来源消息: ("(?:\\.|[^"\\])*"))?$/;

// Legacy notes have no API metadata. Recognize only the complete backend format;
// ordinary messages mentioning a tool or a reference remain ordinary messages.
export function parseToolHistoryNote(
  content: string,
): ToolActivity | undefined {
  const lines = content.trim().split(/\r?\n/);
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
            <pre class="tool-activity-arguments">${escapeHtml(entry.arguments ?? '展开后加载参数')}</pre>
            <button type="button" class="btn btn-ghost btn-sm tool-activity-retry" hidden>重试</button>
          </details>
          ${entry.refId ? `<details class="tool-activity-details" data-tool-ref="${escapeHtml(entry.refId)}" data-tool-name="${escapeHtml(entry.tool)}" data-tool-field="result" data-result-state="idle">
            <summary>工具执行结果</summary>
            <pre class="tool-activity-result">展开后加载结果</pre>
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
    // Status text is kept out of <pre>, which holds only the raw result.
    const status = details.querySelector<HTMLElement>('.tool-activity-status');
    const setStatus = (message: string) => {
      if (!status) return;
      status.textContent = message;
      status.hidden = !message;
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
      if (offset === 0) pre.textContent = copy.loading;
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
            pre.textContent = result.resultRaw || '（无返回内容）';
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
          pre.textContent = result.argumentsAvailable
            ? result.argumentsRaw
            : copy.unavailable;
        }
        details.dataset[stateKey] = 'loaded';
      } catch {
        requests.delete(`${refId}\u0000${isResult ? 1 : 0}\u0000${offset}`);
        if (!active || !details.isConnected) return;
        details.dataset[stateKey] = 'error';
        // Pages already shown stay intact; a failed later page is reported in
        // the status line, so a successful retry yields exactly the raw text.
        if (offset === 0) pre.textContent = copy.failed;
        else setStatus(copy.failed);
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
