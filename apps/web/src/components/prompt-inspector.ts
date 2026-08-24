import { icon } from './icons';
import { escapeHtml } from '../utils/formatters';
import { copyToClipboard } from '../utils/clipboard';
import { toast } from './toast';
import { openDialog } from './dialog';

export interface GroupMessageItem {
  id?: string;
  time?: string;
  sender?: string;
  senderId?: string;
  role?: 'user' | 'assistant';
  content: string;
  rawText: string;
  isSummarized?: boolean;
}

export interface SummaryGroupInfo {
  summary: string;
  messageIds?: string[];
  startMessageId?: string;
  endMessageId?: string;
  startIndex?: number;
  endIndex?: number;
  messages?: string[];
  structuredMessages?: StructuredGroupMessage[];
  parsedMessages?: GroupMessageItem[];
}

export interface StructuredGroupMessage {
  role: string;
  sender: string;
  senderId: string;
  content: string;
  messageId: string;
  time: string;
}

export interface ParsedPrompt {
  model?: string;
  systemPrompt?: string;
  userMessage?: string;
  runningSummary?: string;
  recentMessages: GroupMessageItem[];
  summaryGroups: SummaryGroupInfo[];
  responseContext?: string;
  systemContext?: string;
  replyContext?: string;
  deliveryContext?: string;
  raw: string;
  hasGroupMessages: boolean;
}

/**
 * Parses a single message line into role, time, sender, id, and content.
 */
export function parseMessageLine(line: string): GroupMessageItem {
  if (!line || typeof line !== 'string') {
    return { content: '', rawText: '' };
  }

  let text = line.trim();
  let role: 'user' | 'assistant' | undefined = undefined;
  let time: string | undefined = undefined;
  let id: string | undefined = undefined;

  // Extract role, time, id, and system prefix tags in any order at the beginning of the line
  let matchedTag = true;
  while (matchedTag) {
    matchedTag = false;
    const tagMatch = text.match(/^\[([^\]]+)\]\s*/);
    if (!tagMatch) break;

    const tagContent = tagMatch[1].trim();
    if (/^(?:user|assistant)$/i.test(tagContent)) {
      if (!role) {
        role = tagContent.toLowerCase() as 'user' | 'assistant';
      }
      text = text.slice(tagMatch[0].length);
      matchedTag = true;
    } else if (/^\d{1,2}:\d{2}(?::\d{2})?$/.test(tagContent)) {
      if (!time) {
        time = tagContent;
      }
      text = text.slice(tagMatch[0].length);
      matchedTag = true;
    } else if (/^(?:群消息|group|群聊)$/i.test(tagContent)) {
      text = text.slice(tagMatch[0].length);
      matchedTag = true;
    } else if (/^(?:msg_|id_)[a-zA-Z0-9_-]+$/i.test(tagContent)) {
      if (!id) {
        id = tagContent;
      }
      text = text.slice(tagMatch[0].length);
      matchedTag = true;
    }
  }

  // Sender / ID / Content extraction
  // Handles:
  // - 怪哉GuaiZai (3127306807): 笨笨
  // - 霜降狐: 嗷呜主人...
  // - 张三 (123456) [msg_1]: 周末爬山路线定了吗？
  // - 霜降 [msg_2]: 好的
  const senderMatch = text.match(
    /^([^:\n]+?)(?:\s*\(([^)]+)\))?(?:\s*\[([a-zA-Z0-9_-]+)\])?\s*:\s*([\s\S]*)$/,
  );
  if (senderMatch) {
    let sender = senderMatch[1].trim();
    const senderId = senderMatch[2]?.trim();
    if (!id && senderMatch[3]) {
      id = senderMatch[3].trim();
    }
    const content = senderMatch[4].trim();

    // In case sender still has [user]/[assistant] inside it
    const innerSenderRole = sender.match(/^\[(user|assistant)\]\s*/i);
    if (innerSenderRole) {
      if (!role) {
        role = innerSenderRole[1].toLowerCase() as 'user' | 'assistant';
      }
      sender = sender.slice(innerSenderRole[0].length).trim();
    }

    // Infer role if not explicitly tagged
    if (!role) {
      if (/^(?:assistant|bot|霜降|frostagent)/i.test(sender)) {
        role = 'assistant';
      } else {
        role = 'user';
      }
    }

    return {
      role,
      time,
      sender,
      senderId,
      id,
      content,
      rawText: line,
    };
  }

  // Fallback for plain message line
  if (!role) {
    if (/^(?:assistant|bot|霜降|frostagent)/i.test(text)) {
      role = 'assistant';
    } else {
      role = 'user';
    }
  }

  return {
    role,
    time,
    id,
    content: text,
    rawText: line,
  };
}

function parseStructuredMessageJSONLine(line: string): GroupMessageItem | null {
  if (!line.startsWith('{')) return null;
  try {
    const record = JSON.parse(line) as Record<string, unknown>;
    if (record.role !== 'user' && record.role !== 'assistant') return null;
    if (typeof record.content !== 'string') return null;
    return {
      role: record.role,
      sender: typeof record.sender === 'string' ? record.sender : undefined,
      senderId: typeof record.sender_id === 'string' ? record.sender_id : undefined,
      id: typeof record.message_id === 'string' ? record.message_id : undefined,
      time: typeof record.time === 'string' ? record.time : undefined,
      content: record.content,
      rawText: line,
    };
  } catch {
    return null;
  }
}

function groupMessageFromStructured(record: StructuredGroupMessage): GroupMessageItem {
  return {
    role: record.role === 'assistant' ? 'assistant' : 'user',
    sender: record.sender || undefined,
    senderId: record.senderId || undefined,
    id: record.messageId || undefined,
    time: record.time || undefined,
    content: record.content,
    rawText: JSON.stringify(record),
  };
}

/**
 * Builds a structured ParsedPrompt from backend GetSessionContext response.
 */
export function buildInspectorDataFromSessionContext(context: {
  sessionId: string;
  platform?: string;
  runningSummary?: string;
  recentMessages?: string[];
  summaryGroups?: Array<{
    summary: string;
    messageIds?: string[];
    startMessageId?: string;
    endMessageId?: string;
    startIndex?: number;
    endIndex?: number;
    messages?: string[];
    structuredMessages?: StructuredGroupMessage[];
  }>;
  promptText?: string;
  systemPrompt?: string;
  model?: string;
  recentStructuredMessages?: StructuredGroupMessage[];
}): ParsedPrompt {
  const summaryGroups: SummaryGroupInfo[] = (context.summaryGroups || []).map((g) => {
    const rawMsgs = g.messages || [];
    return {
      summary: g.summary,
      messageIds: g.messageIds,
      startMessageId: g.startMessageId,
      endMessageId: g.endMessageId,
      startIndex: g.startIndex,
      endIndex: g.endIndex,
      messages: rawMsgs,
      structuredMessages: g.structuredMessages,
      parsedMessages: (g.structuredMessages?.length
        ? g.structuredMessages.map(groupMessageFromStructured)
        : rawMsgs.map(parseMessageLine)
      ).map((item) => ({ ...item, isSummarized: true })),
    };
  });

  const recentMessages: GroupMessageItem[] = (context.recentStructuredMessages?.length
    ? context.recentStructuredMessages.map(groupMessageFromStructured)
    : (context.recentMessages || []).map(parseMessageLine)
  ).map((item) => ({ ...item, isSummarized: false }));

  return {
    raw: context.promptText || '',
    model: context.model,
    systemPrompt: context.systemPrompt,
    runningSummary: context.runningSummary,
    recentMessages,
    summaryGroups,
    hasGroupMessages: recentMessages.length > 0 || summaryGroups.some((g) => (g.parsedMessages?.length ?? 0) > 0),
    responseContext: `当前会话: ${context.sessionId}\n平台: ${context.platform || 'unknown'}`,
  };
}

/**
 * Parses raw LLM prompt string (JSON chat request, raw text with XML tags, or prompt trace)
 * into a structured ParsedPrompt object.
 */
export function parsePrompt(raw: string): ParsedPrompt {
  const result: ParsedPrompt = {
    raw,
    recentMessages: [],
    summaryGroups: [],
    hasGroupMessages: false,
  };

  if (!raw || typeof raw !== 'string') {
    return result;
  }

  let textToParse = raw.trim();

  // 1. Try parsing as JSON (OpenAI Chat Request format)
  if (textToParse.startsWith('{') && textToParse.endsWith('}')) {
    try {
      const json = JSON.parse(textToParse) as Record<string, unknown>;
      if (json.model && typeof json.model === 'string') {
        result.model = json.model;
      }
      if (Array.isArray(json.summary_groups)) {
        result.summaryGroups = json.summary_groups as SummaryGroupInfo[];
      } else if (Array.isArray(json.summaryGroups)) {
        result.summaryGroups = json.summaryGroups as SummaryGroupInfo[];
      }

      if (Array.isArray(json.messages)) {
        let lastUserContent: string | undefined;
        for (const m of json.messages as Array<{ role?: string; content?: unknown }>) {
          const role = m.role || 'unknown';
          let contentStr = '';
          if (typeof m.content === 'string') {
            contentStr = m.content;
          } else if (Array.isArray(m.content)) {
            contentStr = m.content
              .map((c: { text?: string }) => c.text || '')
              .filter(Boolean)
              .join('\n');
          }

          if (role === 'system') {
            result.systemPrompt = result.systemPrompt
              ? `${result.systemPrompt}\n\n${contentStr}`
              : contentStr;
          } else if (role === 'user') {
            lastUserContent = contentStr;
          }
        }
        textToParse = lastUserContent ?? '';
      }
    } catch {
      // Not valid JSON, continue with raw text parsing
    }
  }

  // 2. Extract XML-like prompt tags from text
  const runningSummaryMatch = textToParse.match(
    /<group_running_summary>([\s\S]*?)<\/group_running_summary>/i,
  );
  if (runningSummaryMatch) {
    result.runningSummary = runningSummaryMatch[1].trim();
  }

  const responseContextMatch = textToParse.match(
    /<response_context>([\s\S]*?)<\/response_context>/i,
  );
  if (responseContextMatch) {
    result.responseContext = responseContextMatch[1].trim();
  }

  const systemContextMatch = textToParse.match(
    /<system_context>([\s\S]*?)<\/system_context>/i,
  );
  if (systemContextMatch) {
    result.systemContext = systemContextMatch[1].trim();
  }

  const replyContextMatch = textToParse.match(
    /<reply_context>([\s\S]*?)<\/reply_context>/i,
  );
  if (replyContextMatch) {
    result.replyContext = replyContextMatch[1].trim();
  }

  const deliveryContextMatch = textToParse.match(
    /<delivery_context>([\s\S]*?)<\/delivery_context>/i,
  );
  if (deliveryContextMatch) {
    result.deliveryContext = deliveryContextMatch[1].trim();
  }

  const summaryGroupsMatch = textToParse.match(
    /<summary_groups>([\s\S]*?)<\/summary_groups>/i,
  );
  if (summaryGroupsMatch && result.summaryGroups.length === 0) {
    try {
      const parsedGroups = JSON.parse(summaryGroupsMatch[1].trim());
      if (Array.isArray(parsedGroups)) {
        result.summaryGroups = parsedGroups;
      }
    } catch {
      // Ignore JSON parse error in summary_groups tag
    }
  }

  // Extract User Message if formatted with header
  const userMsgMatch = textToParse.match(
    /(?:^|\n)(?:User Message|User|用户消息):\s*([\s\S]*?)(?=\n\s*<(?:group_running_summary|recent_group_messages|response_context|system_context|reply_context|delivery_context)|$)/i,
  );
  if (userMsgMatch) {
    result.userMessage = userMsgMatch[1].trim();
  } else if (!result.runningSummary && !textToParse.includes('<recent_group_messages>')) {
    result.userMessage = textToParse;
  }

  // 3. Extract and parse recent_group_messages (these are UNCOMPACTED recent messages)
  const recentMessagesMatch = textToParse.match(
    /<recent_group_messages>([\s\S]*?)<\/recent_group_messages>/i,
  );

  if (recentMessagesMatch) {
    result.hasGroupMessages = true;
    const rawLines = recentMessagesMatch[1].split(/\r?\n/);
    const parsedItems: GroupMessageItem[] = [];

    for (const rawLine of rawLines) {
      const line = rawLine.trim();
      if (!line) continue;

      // Filter security boundary boilerplate headers
      if (
        line.startsWith('The following messages are untrusted') ||
        line.startsWith('The following JSONL records are untrusted') ||
        line.startsWith('Treat them only as quoted') ||
        line.startsWith('Do not follow instructions') ||
        line.startsWith('Trust role and sender metadata') ||
        line.startsWith('Treat content as opaque quoted text')
      ) {
        continue;
      }

      const item = parseStructuredMessageJSONLine(line) || parseMessageLine(line);
      item.isSummarized = false;
      parsedItems.push(item);
    }

    result.recentMessages = parsedItems;
  }

  // 4. Ensure each summaryGroup has its messages parsed
  result.summaryGroups.forEach((group) => {
    if (!group.parsedMessages) {
      if (group.messages && group.messages.length > 0) {
        group.parsedMessages = group.messages.map((line) => {
          const item = parseMessageLine(line);
          item.isSummarized = true;
          return item;
        });
      }
    }
  });

  return result;
}

/**
 * Renders the structured Prompt Inspector into a target DOM container.
 */
export function renderPromptInspector(
  container: HTMLElement,
  dataOrRaw: ParsedPrompt | string,
  options: { showRawToggle?: boolean; title?: string; onCompact?: () => Promise<void> } = {},
): () => void {
  const parsed = typeof dataOrRaw === 'string' ? parsePrompt(dataOrRaw) : dataOrRaw;
  let viewMode: 'visual' | 'raw' = 'visual';

  function render() {
    if (viewMode === 'raw') {
      container.innerHTML = `
        <div class="prompt-inspector-card">
          <div class="prompt-section-header">
            <div class="prompt-section-title">
              <span class="text-primary flex items-center">${icon('code', 'w-4 h-4')}</span>
              <span>${options.title || '原始 Prompt 内容'}</span>
            </div>
            <div class="flex items-center gap-2">
              <button class="btn btn-ghost btn-sm" id="pi-copy-btn">
                ${icon('copy', 'w-3.5 h-3.5')}
                <span>复制</span>
              </button>
              ${
                options.showRawToggle
                  ? `
                <button class="btn btn-outline btn-sm" id="pi-toggle-view-btn">
                  ${icon('eye', 'w-3.5 h-3.5')}
                  <span>切换可视化</span>
                </button>
              `
                  : ''
              }
            </div>
          </div>
          <pre class="card p-3 bg-muted text-xs font-mono whitespace-pre-wrap select-text leading-relaxed text-foreground" style="max-height: 30rem; overflow-y: auto;">${escapeHtml(
            parsed.raw || '（无内容）',
          )}</pre>
        </div>
      `;
      bindRawEvents();
      return;
    }

    const totalSummarizedMsgs = parsed.summaryGroups.reduce(
      (acc, g) => acc + (g.parsedMessages?.length || g.messages?.length || 0),
      0,
    );

    container.innerHTML = `
      <div class="prompt-inspector-card">
        <!-- Top Toolbar / Badges -->
        <div class="flex items-center justify-between gap-3 flex-wrap border-b border-border pb-2.5">
          <div class="flex items-center gap-2 flex-wrap text-xs">
            <span class="badge badge-outline flex items-center gap-1">
              ${icon('sparkles', 'w-3 h-3 text-info')}
              <span>Prompt Inspector</span>
            </span>
            ${
              parsed.model
                ? `<span class="badge badge-purple text-xs font-mono">${escapeHtml(parsed.model)}</span>`
                : ''
            }
            ${
              parsed.summaryGroups.length > 0
                ? `<span class="badge badge-outline text-xs text-info border-info/30 bg-info/5">${
                    parsed.summaryGroups.length === 1
                      ? `最新压缩批次 (${totalSummarizedMsgs} 条消息)`
                      : `${parsed.summaryGroups.length} 个摘要分组 (${totalSummarizedMsgs} 条消息)`
                  }</span>`
                : ''
            }
            ${
              parsed.recentMessages.length > 0
                ? `<span class="badge badge-info text-xs">${parsed.recentMessages.length} 条未压缩消息</span>`
                : ''
            }
          </div>
          <div class="flex items-center gap-2">
            <button class="btn btn-ghost btn-sm" id="pi-copy-btn" title="复制 Prompt">
              ${icon('copy', 'w-3.5 h-3.5')}
              <span>复制</span>
            </button>
            ${
              options.showRawToggle
                ? `
              <button class="btn btn-outline btn-sm" id="pi-toggle-view-btn" title="查看原始文本">
                ${icon('code', 'w-3.5 h-3.5')}
                <span>原始文本</span>
              </button>
            `
                : ''
            }
          </div>
        </div>

        <!-- Trigger User Message -->
        ${
          parsed.userMessage
            ? `
          <div class="prompt-section">
            <div class="prompt-section-header">
              <div class="prompt-section-title">
                <span class="text-primary flex items-center">${icon('chat', 'w-3.5 h-3.5')}</span>
                <span>当前触发消息 (User Message)</span>
              </div>
            </div>
            <div class="card p-3 text-xs leading-relaxed select-text bg-muted/40 text-foreground whitespace-pre-wrap">${escapeHtml(
              parsed.userMessage,
            )}</div>
          </div>
        `
            : ''
        }

        <!-- Section 1: Latest summarized message batch and current compact content -->
        ${
          parsed.summaryGroups.length > 0
            ? `
          <div class="prompt-section">
            <div class="prompt-section-header flex-wrap">
              <div class="prompt-section-title flex-wrap">
                <span class="text-info flex items-center">${icon('sparkles', 'w-3.5 h-3.5')}</span>
                <span>最新压缩批次 (最近一次滚动总结)</span>
                ${
                  options.onCompact
                    ? `<button
                        class="btn btn-outline btn-sm text-xs h-7 px-2.5 ml-1"
                        id="pi-compact-now-btn"
                        title="立即将当前未压缩消息合并到滚动总结"
                        ${parsed.recentMessages.length === 0 ? 'disabled' : ''}
                      >
                        ${icon('play', 'w-3 h-3')}
                        <span>立即 compact 上下文</span>
                      </button>`
                    : ''
                }
              </div>
              <span class="text-xs text-muted">在右侧卡片展开查看当前群聊 compact 内容</span>
            </div>
            <div class="prompt-section-body">
              <div class="group-messages-container">
                ${renderSummaryGroups(parsed.summaryGroups)}
              </div>
            </div>
          </div>
        `
            : ''
        }

        <!-- Section 2: Fallback Running Summary (If running summary exists but no explicit groups) -->
        ${
          parsed.summaryGroups.length === 0 && parsed.runningSummary
            ? `
          <div class="prompt-section">
            <div class="prompt-section-header">
              <div class="prompt-section-title">
                <span class="text-info flex items-center">${icon('sparkles', 'w-3.5 h-3.5')}</span>
                <span>群聊滚动摘要 (group_running_summary)</span>
              </div>
            </div>
            <div class="card p-3 text-xs leading-relaxed select-text bg-info-bg/30 border-info-border/40 text-foreground whitespace-pre-wrap">${escapeHtml(
              parsed.runningSummary,
            )}</div>
          </div>
        `
            : ''
        }

        <!-- Section 3: Pending / Uncompacted Recent Group Messages -->
        ${
          parsed.recentMessages.length > 0
            ? `
          <div class="prompt-section">
            <div class="prompt-section-header">
              <div class="prompt-section-title">
                <span class="text-primary flex items-center">${icon('users', 'w-3.5 h-3.5')}</span>
                <span>当前未压缩消息 (recent_group_messages)</span>
              </div>
              <span class="text-xs text-muted">待进入下一次滚动压缩的最新群消息</span>
            </div>
            <div class="prompt-section-body">
              <div class="group-messages-container">
                ${parsed.recentMessages.map((msg) => renderSingleMessageRow(msg, false)).join('')}
              </div>
            </div>
          </div>
        `
            : ''
        }

        <!-- Delivery Context (if previous assistant message failed to deliver) -->
        ${
          parsed.deliveryContext
            ? `
          <div class="prompt-section">
            <div class="prompt-section-header">
              <div class="prompt-section-title">
                <span class="text-amber-500 flex items-center">${icon('alert_circle', 'w-3.5 h-3.5')}</span>
                <span>未送达上下文 (delivery_context)</span>
              </div>
            </div>
            <div class="card p-3 text-xs leading-relaxed select-text bg-amber-500/10 border-amber-500/30 text-amber-900 dark:text-amber-200 whitespace-pre-wrap">${escapeHtml(
              parsed.deliveryContext,
            )}</div>
          </div>
        `
            : ''
        }

        <!-- Response Context / System Context / Reply Context (if any) -->
        ${
          parsed.replyContext
            ? `
          <div class="prompt-section">
            <div class="prompt-section-header">
              <div class="prompt-section-title">
                <span class="text-primary flex items-center">${icon('message_square', 'w-3.5 h-3.5')}</span>
                <span>回复上下文 (reply_context)</span>
              </div>
            </div>
            <div class="card p-3 text-xs leading-relaxed select-text bg-muted/40 text-foreground whitespace-pre-wrap">${escapeHtml(
              parsed.replyContext,
            )}</div>
          </div>
        `
            : ''
        }

        ${
          parsed.responseContext
            ? `
          <div class="prompt-section">
            <div class="prompt-section-header">
              <div class="prompt-section-title">
                <span class="text-primary flex items-center">${icon('info', 'w-3.5 h-3.5')}</span>
                <span>响应上下文 (response_context)</span>
              </div>
            </div>
            <div class="card p-3 text-xs leading-relaxed select-text bg-muted/40 text-foreground whitespace-pre-wrap">${escapeHtml(
              parsed.responseContext,
            )}</div>
          </div>
        `
            : ''
        }

        ${
          parsed.systemContext
            ? `
          <div class="prompt-section">
            <div class="prompt-section-header">
              <div class="prompt-section-title">
                <span class="text-primary flex items-center">${icon('info', 'w-3.5 h-3.5')}</span>
                <span>系统上下文 (system_context)</span>
              </div>
            </div>
            <div class="card p-3 text-xs leading-relaxed select-text bg-muted/40 text-foreground whitespace-pre-wrap">${escapeHtml(
              parsed.systemContext,
            )}</div>
          </div>
        `
            : ''
        }

        <!-- System Prompt -->
        ${
          parsed.systemPrompt
            ? `
          <details class="prompt-section">
            <summary class="prompt-section-header cursor-pointer select-none py-1">
              <div class="prompt-section-title">
                <span class="text-primary flex items-center">${icon('sparkles', 'w-3.5 h-3.5')}</span>
                <span>System Prompt</span>
              </div>
              <span class="text-xs text-muted">点击展开/折叠</span>
            </summary>
            <pre class="card p-3 mt-1 bg-muted/40 text-xs font-mono whitespace-pre-wrap select-text leading-relaxed text-foreground" style="max-height: 18rem; overflow-y: auto;">${escapeHtml(
              parsed.systemPrompt,
            )}</pre>
          </details>
        `
            : ''
        }
      </div>
    `;

    bindInteractiveEvents();
  }

  function bindRawEvents() {
    const copyBtn = container.querySelector<HTMLButtonElement>('#pi-copy-btn');
    copyBtn?.addEventListener('click', async () => {
      const success = await copyToClipboard(parsed.raw);
      if (success) toast.success('已复制 Prompt 内容');
      else toast.error('复制失败，请手动复制');
    });

    const toggleBtn = container.querySelector<HTMLButtonElement>('#pi-toggle-view-btn');
    toggleBtn?.addEventListener('click', () => {
      viewMode = 'visual';
      render();
    });
  }

  function bindInteractiveEvents() {
    const copyBtn = container.querySelector<HTMLButtonElement>('#pi-copy-btn');
    copyBtn?.addEventListener('click', async () => {
      const success = await copyToClipboard(parsed.raw);
      if (success) toast.success('已复制 Prompt 内容');
      else toast.error('复制失败，请手动复制');
    });

    const toggleBtn = container.querySelector<HTMLButtonElement>('#pi-toggle-view-btn');
    toggleBtn?.addEventListener('click', () => {
      viewMode = 'raw';
      render();
    });

    const compactBtn = container.querySelector<HTMLButtonElement>('#pi-compact-now-btn');
    compactBtn?.addEventListener('click', async () => {
      if (!options.onCompact || compactBtn.disabled) return;

      compactBtn.disabled = true;
      compactBtn.innerHTML = `<span class="spinner"></span><span>compact 中...</span>`;
      try {
        await options.onCompact();
      } catch (err) {
        toast.error('立即 compact 失败: ' + (err instanceof Error ? err.message : String(err)));
        if (compactBtn.isConnected) {
          compactBtn.disabled = false;
          compactBtn.innerHTML = `${icon('play', 'w-3 h-3')}<span>立即 compact 上下文</span>`;
        }
      }
    });

  }

  render();

  return () => {
    // Cleanup if needed
  };
}

/**
 * Renders all summary groups with their current compact content.
 */
function renderSummaryGroups(summaryGroups: SummaryGroupInfo[]): string {
  return summaryGroups
    .map((group, idx) => {
      const messages = group.parsedMessages || (group.messages || []).map(parseMessageLine);
      return renderSummaryGroupBlock(messages, group, idx);
    })
    .join('');
}

/**
 * Renders a single summary group block with an expandable compact card.
 */
function renderSummaryGroupBlock(
  messages: GroupMessageItem[],
  group: SummaryGroupInfo,
  groupIdx: number,
): string {
  const rowsHtml = messages.map((m) => renderSingleMessageRow(m, true)).join('');

  return `
    <div class="summary-group-wrapper" data-group-index="${groupIdx}">
      <!-- Latest compacted message batch -->
      <div class="summary-group-messages">
        ${rowsHtml}
      </div>

      <!-- Expandable current group compact content -->
      <details class="summary-compact-card">
        <summary class="summary-compact-card-header">
          <span class="summary-compact-card-title-wrap">
            <span class="summary-compact-card-icon">${icon('sparkles', 'w-3.5 h-3.5')}</span>
            <span class="summary-compact-card-title">当前群聊 compact 内容</span>
          </span>
          <span class="summary-compact-card-meta">
            <span>${messages.length} 条消息</span>
            <span class="summary-compact-card-chevron" aria-hidden="true">${icon('chevron_down', 'w-3.5 h-3.5')}</span>
          </span>
        </summary>
        <div class="summary-compact-card-body select-text">${escapeHtml(group.summary || '暂无 compact 内容')}</div>
      </details>
    </div>
  `;
}

/**
 * Renders an individual message entry row.
 */
function renderSingleMessageRow(msg: GroupMessageItem, isSummarized: boolean): string {
  const uncompactedClass = isSummarized ? '' : 'is-uncompacted';
  const isAssistant = msg.role === 'assistant';
  const roleClass = isAssistant ? 'is-assistant' : 'is-user';
  const defaultSender = isAssistant ? '霜降' : '用户';
  const displaySender = msg.sender || defaultSender;

  return `
    <div class="group-msg-row ${uncompactedClass} ${roleClass}" ${msg.id ? `data-msg-id="${escapeHtml(msg.id)}"` : ''}>
      <div class="msg-meta">
        ${
          isAssistant
            ? `<span class="badge badge-purple msg-role-badge text-[10px] py-0 px-1 font-medium">Assistant / Bot</span>`
            : `<span class="badge badge-outline msg-role-badge text-[10px] py-0 px-1 font-medium text-muted">User</span>`
        }
        ${msg.time ? `<span class="msg-time">${escapeHtml(msg.time)}</span>` : ''}
        <span class="msg-sender ${isAssistant ? 'text-purple-600 dark:text-purple-400 font-semibold' : ''}">${escapeHtml(displaySender)}</span>
        ${
          msg.senderId
            ? `<span class="text-[10px] text-muted font-mono">(${escapeHtml(msg.senderId)})</span>`
            : ''
        }
        ${msg.id ? `<span class="msg-id">#${escapeHtml(msg.id)}</span>` : ''}
      </div>
      <div class="msg-content select-text ${isAssistant ? 'text-foreground/95' : ''}">${escapeHtml(msg.content)}</div>
    </div>
  `;
}

/**
 * Opens a full-featured modal dialog for Prompt inspection.
 */
export function openPromptInspectorDialog(raw: string, title = 'Prompt Inspector'): void {
  openDialog({
    title,
    description: '查看发送给 LLM 的结构化上下文、群消息历史与摘要分组',
    maxWidth: '54rem',
    bodyHtml: `<div id="dialog-prompt-inspector-mount"></div>`,
    footerHtml: `
      <button class="btn btn-outline btn-sm" id="pi-dialog-close-btn">
        <span>关闭</span>
      </button>
    `,
    onMount: (dialogEl, close) => {
      const mountEl = dialogEl.querySelector<HTMLElement>('#dialog-prompt-inspector-mount');
      if (mountEl) {
        renderPromptInspector(mountEl, raw, { showRawToggle: true, title });
      }
      dialogEl.querySelector('#pi-dialog-close-btn')?.addEventListener('click', close);
    },
  });
}
