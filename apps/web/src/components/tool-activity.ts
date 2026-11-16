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
  raw?: string;
}

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

function shortRef(value: string): string {
  return value.startsWith('fa_toolcall_') ? `…${value.slice(-8)}` : value;
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
      <span class="tool-activity-preview">${[...names].map(([name, count]) => escapeHtml(name) + (count > 1 ? ` ×${count}` : '')).join(' · ')}</span>
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
          const details = [
            ['本次引用', entry.refId],
            ['读取的引用', entry.readTargetRefId],
            ['调用 ID', entry.callId],
            ['调用参数', entry.arguments],
            ['返回原文', entry.result],
          ].filter((item) => item[1] !== undefined);
          return `<div class="tool-activity-entry" data-kind="${read ? 'history-read' : 'tool-call'}">
          <div class="tool-activity-entry-title"><span class="tool-activity-order">${index + 1}</span><span class="tool-activity-kind">${label}</span><code>${escapeHtml(entry.tool)}</code></div>
          ${entry.readTargetRefId ? `<div class="tool-activity-target">读取 → <code title="${escapeHtml(entry.readTargetRefId)}">${escapeHtml(shortRef(entry.readTargetRefId))}</code></div>` : ''}
          <details class="tool-activity-details"><summary>完整引用${entry.arguments !== undefined ? '与参数' : entry.result !== undefined ? '与返回原文' : ''}</summary>
            <dl>${details.map(([label, value]) => `<dt>${label}</dt><dd><pre>${escapeHtml(value || '')}</pre></dd>`).join('')}</dl>
          </details>
        </div>`;
        })
        .join('')}
      ${!activity.entries.length && activity.raw ? `<pre class="tool-activity-raw">${escapeHtml(activity.raw)}</pre>` : ''}
    </div>
  </details>`;
}
