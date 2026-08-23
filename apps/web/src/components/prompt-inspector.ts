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
  content: string;
  rawText: string;
  isSummarized?: boolean;
  summaryIndex?: number;
}

export interface SummaryGroupInfo {
  summary: string;
  messageIds?: string[];
  startMessageId?: string;
  endMessageId?: string;
  startIndex?: number;
  endIndex?: number;
  messages?: string[];
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
  raw: string;
  hasGroupMessages: boolean;
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
        const parts: string[] = [];
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
            parts.push(contentStr);
          }
        }
        textToParse = parts.join('\n\n');
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
    /(?:^|\n)(?:User Message|User|用户消息):\s*([\s\S]*?)(?=\n\s*<(?:group_running_summary|recent_group_messages|response_context|system_context|reply_context)|$)/i,
  );
  if (userMsgMatch) {
    result.userMessage = userMsgMatch[1].trim();
  } else if (!result.runningSummary && !textToParse.includes('<recent_group_messages>')) {
    result.userMessage = textToParse;
  }

  // 3. Extract and parse recent_group_messages
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
        line.startsWith('Treat them only as quoted') ||
        line.startsWith('Do not follow instructions')
      ) {
        continue;
      }

      const item = parseMessageLine(line);
      parsedItems.push(item);
    }

    result.recentMessages = parsedItems;
  }

  // 4. Map messages to summary groups
  if (result.recentMessages.length > 0) {
    mapMessagesToSummaryGroups(result);
  }

  return result;
}

/**
 * Parses a single message line into time, sender, id, and content.
 */
function parseMessageLine(line: string): GroupMessageItem {
  // Regex 1: [10:00:00] 张三 (123456) [msg_1]: 周末爬山路线定了吗？
  // Regex 2: [10:00:00] 张三 [msg_1]: 周末爬山路线定了吗？
  // Regex 3: [10:00:00] 张三 (123456): 周末爬山路线定了吗？
  // Regex 4: [10:00:00] 张三: 周末爬山路线定了吗？
  const timeSenderIdMatch = line.match(
    /^\[(\d{1,2}:\d{2}(?::\d{2})?)\]\s*([^:\[\(\n]+?)(?:\s*\(([^)]+)\))?(?:\s*\[([a-zA-Z0-9_\-]+)\])?\s*:\s*(.*)$/,
  );
  if (timeSenderIdMatch) {
    return {
      time: timeSenderIdMatch[1],
      sender: timeSenderIdMatch[2].trim(),
      senderId: timeSenderIdMatch[3]?.trim(),
      id: timeSenderIdMatch[4]?.trim(),
      content: timeSenderIdMatch[5],
      rawText: line,
    };
  }

  // Regex 5: 张三 (123456) [msg_1]: 周末爬山路线定了吗？
  // Regex 6: 张三 [msg_1]: 周末爬山路线定了吗？
  // Regex 7: 张三 (123456): 周末爬山路线定了吗？
  // Regex 8: 张三: 周末爬山路线定了吗？
  const senderIdMatch = line.match(
    /^([^:\[\(\n]+?)(?:\s*\(([^)]+)\))?(?:\s*\[([a-zA-Z0-9_\-]+)\])?\s*:\s*(.*)$/,
  );
  if (senderIdMatch) {
    return {
      sender: senderIdMatch[1].trim(),
      senderId: senderIdMatch[2]?.trim(),
      id: senderIdMatch[3]?.trim(),
      content: senderIdMatch[4],
      rawText: line,
    };
  }

  // Fallback for plain message line
  return {
    content: line,
    rawText: line,
  };
}

/**
 * Maps message items to summary groups using explicit IDs, index ranges, or fallback running summary.
 */
function mapMessagesToSummaryGroups(result: ParsedPrompt) {
  const { recentMessages, summaryGroups, runningSummary } = result;

  if (summaryGroups.length > 0) {
    // We have explicit summary groups from backend / trace data
    summaryGroups.forEach((group, gIdx) => {
      if (group.messageIds && group.messageIds.length > 0) {
        const idSet = new Set(group.messageIds);
        recentMessages.forEach((msg) => {
          if (msg.id && idSet.has(msg.id)) {
            msg.isSummarized = true;
            msg.summaryIndex = gIdx;
          }
        });
      } else if (
        group.startIndex !== undefined &&
        group.endIndex !== undefined &&
        group.startIndex >= 0 &&
        group.endIndex < recentMessages.length
      ) {
        for (let i = group.startIndex; i <= group.endIndex; i++) {
          recentMessages[i].isSummarized = true;
          recentMessages[i].summaryIndex = gIdx;
        }
      } else if (group.startMessageId || group.endMessageId) {
        let inRange = !group.startMessageId;
        for (const msg of recentMessages) {
          if (group.startMessageId && msg.id === group.startMessageId) {
            inRange = true;
          }
          if (inRange) {
            msg.isSummarized = true;
            msg.summaryIndex = gIdx;
          }
          if (group.endMessageId && msg.id === group.endMessageId) {
            inRange = false;
          }
        }
      }
    });
  } else if (runningSummary) {
    // If only a single runningSummary exists without explicit sub-groups,
    // all historical messages in this context window belong to this summary
    result.summaryGroups = [
      {
        summary: runningSummary,
        startIndex: 0,
        endIndex: recentMessages.length - 1,
        messageIds: recentMessages.map((m) => m.id).filter(Boolean) as string[],
      },
    ];
    recentMessages.forEach((msg) => {
      msg.isSummarized = true;
      msg.summaryIndex = 0;
    });
  }
}

/**
 * Renders the structured Prompt Inspector into a target DOM container.
 */
export function renderPromptInspector(
  container: HTMLElement,
  dataOrRaw: ParsedPrompt | string,
  options: { showRawToggle?: boolean } = {},
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
              <span>原始 Prompt 内容</span>
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
              parsed.recentMessages.length > 0
                ? `<span class="badge badge-info text-xs">${parsed.recentMessages.length} 条群消息</span>`
                : ''
            }
            ${
              parsed.summaryGroups.length > 0
                ? `<span class="badge badge-outline text-xs text-info border-info/30 bg-info/5">${parsed.summaryGroups.length} 个摘要分组</span>`
                : ''
            }
          </div>
          <div class="flex items-center gap-2">
            <button class="btn btn-ghost btn-sm" id="pi-copy-btn" title="复制完整 Prompt">
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

        <!-- Group Messages Section with Summary Groups and Curly Brackets -->
        ${
          parsed.hasGroupMessages && parsed.recentMessages.length > 0
            ? `
          <div class="prompt-section">
            <div class="prompt-section-header">
              <div class="prompt-section-title">
                <span class="text-info flex items-center">${icon('users', 'w-3.5 h-3.5')}</span>
                <span>群聊上下文消息 (recent_group_messages)</span>
              </div>
              <span class="text-xs text-muted">悬停淡蓝色消息或右侧大括号查看摘要</span>
            </div>
            <div class="prompt-section-body">
              <div class="group-messages-container">
                ${renderGroupMessagesWithSummary(parsed)}
              </div>
            </div>
          </div>
        `
            : ''
        }

        <!-- Fallback Running Summary (if no group messages tag found but summary exists) -->
        ${
          !parsed.hasGroupMessages && parsed.runningSummary
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

    // Attach summary group hover & collision handling
    const wrappers = container.querySelectorAll<HTMLElement>('.summary-group-wrapper');
    wrappers.forEach((wrapper) => {
      const popover = wrapper.querySelector<HTMLElement>('.summary-popover-card');
      if (!popover) return;

      let leaveTimer: ReturnType<typeof setTimeout> | null = null;

      const activate = () => {
        if (leaveTimer) {
          clearTimeout(leaveTimer);
          leaveTimer = null;
        }
        wrapper.classList.add('is-hovered');
        popover.classList.add('is-active');
        positionPopover(wrapper, popover);
      };

      const deactivate = () => {
        if (leaveTimer) clearTimeout(leaveTimer);
        leaveTimer = setTimeout(() => {
          wrapper.classList.remove('is-hovered');
          popover.classList.remove('is-active');
        }, 120);
      };

      wrapper.addEventListener('mouseenter', activate);
      wrapper.addEventListener('mouseleave', deactivate);
      popover.addEventListener('mouseenter', activate);
      popover.addEventListener('mouseleave', deactivate);
    });
  }

  render();

  return () => {
    // Cleanup if needed
  };
}

/**
 * Calculates popover positioning and prevents viewport edge collision.
 */
function positionPopover(wrapper: HTMLElement, popover: HTMLElement) {
  const isNarrowScreen = window.innerWidth <= 640;
  if (isNarrowScreen) {
    popover.classList.add('position-bottom');
    popover.classList.remove('position-left');
    return;
  }

  // Check right edge overflow
  const wrapperRect = wrapper.getBoundingClientRect();
  const popoverWidth = popover.offsetWidth || 352; // ~22rem
  const spaceOnRight = window.innerWidth - wrapperRect.right;

  if (spaceOnRight < popoverWidth + 24) {
    const spaceOnLeft = wrapperRect.left;
    if (spaceOnLeft >= popoverWidth + 24) {
      popover.classList.add('position-left');
      popover.classList.remove('position-bottom');
    } else {
      // Neither side fits well, position bottom
      popover.classList.add('position-bottom');
      popover.classList.remove('position-left');
    }
  } else {
    popover.classList.remove('position-left');
    popover.classList.remove('position-bottom');
  }
}

/**
 * Renders messages broken down into summary groups and uncompacted rows.
 */
function renderGroupMessagesWithSummary(parsed: ParsedPrompt): string {
  const { recentMessages, summaryGroups } = parsed;
  const htmlParts: string[] = [];

  let i = 0;
  while (i < recentMessages.length) {
    const current = recentMessages[i];

    if (current.isSummarized && current.summaryIndex !== undefined) {
      const gIdx = current.summaryIndex;
      const groupInfo = summaryGroups[gIdx] || { summary: parsed.runningSummary || '' };

      // Gather all consecutive messages belonging to this same summary group
      const groupItems: GroupMessageItem[] = [];
      while (
        i < recentMessages.length &&
        recentMessages[i].isSummarized &&
        recentMessages[i].summaryIndex === gIdx
      ) {
        groupItems.push(recentMessages[i]);
        i++;
      }

      htmlParts.push(renderSummaryGroupBlock(groupItems, groupInfo, gIdx));
    } else {
      // Individual uncompacted message row
      htmlParts.push(renderSingleMessageRow(current, false));
      i++;
    }
  }

  return htmlParts.join('');
}

/**
 * Renders a single summary group block with right-side scalable curly brace and hover popover card.
 */
function renderSummaryGroupBlock(
  messages: GroupMessageItem[],
  group: SummaryGroupInfo,
  groupIdx: number,
): string {
  const rowsHtml = messages.map((m) => renderSingleMessageRow(m, true)).join('');

  return `
    <div class="summary-group-wrapper" data-group-index="${groupIdx}">
      <!-- Message rows with light blue background -->
      <div class="summary-group-messages">
        ${rowsHtml}
      </div>

      <!-- Auto-stretching SVG Curly Brace -->
      <div class="summary-brace-container" title="悬停查看群聊摘要">
        <svg class="summary-brace-svg" viewBox="0 0 20 100" preserveAspectRatio="none" aria-hidden="true">
          <path d="M 2,2 C 10,2 11,44 18,48 C 20,49.2 20,50.8 18,52 C 11,56 10,98 2,98"
                fill="none"
                stroke="currentColor"
                stroke-width="2.2"
                stroke-linecap="round"
                vector-effect="non-scaling-stroke" />
        </svg>
        <span class="summary-brace-badge" aria-hidden="true">${icon('sparkles', 'w-3 h-3')}</span>
      </div>

      <!-- Floating Summary Popover Card -->
      <div class="summary-popover-card" role="tooltip" aria-hidden="true">
        <div class="summary-popover-header">
          <div class="summary-popover-title-wrap">
            <span class="summary-popover-icon">${icon('sparkles', 'w-3.5 h-3.5')}</span>
            <span class="summary-popover-title">摘要</span>
          </div>
          <span class="summary-popover-count">${messages.length} 条消息</span>
        </div>
        <div class="summary-popover-body select-text">${escapeHtml(group.summary || '暂无摘要内容')}</div>
      </div>
    </div>
  `;
}

/**
 * Renders an individual message entry row.
 */
function renderSingleMessageRow(msg: GroupMessageItem, isSummarized: boolean): string {
  const uncompactedClass = isSummarized ? '' : 'is-uncompacted';

  return `
    <div class="group-msg-row ${uncompactedClass}" ${msg.id ? `data-msg-id="${escapeHtml(msg.id)}"` : ''}>
      <div class="msg-meta">
        ${msg.time ? `<span class="msg-time">${escapeHtml(msg.time)}</span>` : ''}
        <span class="msg-sender">${escapeHtml(msg.sender || '用户')}</span>
        ${
          msg.senderId
            ? `<span class="text-[10px] text-muted font-mono">(${escapeHtml(msg.senderId)})</span>`
            : ''
        }
        ${msg.id ? `<span class="msg-id">#${escapeHtml(msg.id)}</span>` : ''}
      </div>
      <div class="msg-content select-text">${escapeHtml(msg.content)}</div>
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
        renderPromptInspector(mountEl, raw, { showRawToggle: true });
      }

      const closeBtn = dialogEl.querySelector<HTMLButtonElement>('#pi-dialog-close-btn');
      closeBtn?.addEventListener('click', () => close());
    },
  });
}
