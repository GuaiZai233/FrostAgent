import type { createInstanceAPI } from '../api/client';
import { escapeHtml, formatDateTime, PageTokenStack } from '../utils/formatters';
import { openDialog } from './dialog';
import { icon } from './icons';
import { DEFAULT_BOT_NAME, renderSingleMessageRow } from './prompt-inspector';

// Match the session key shapes accepted by botstatus.chatHistoryScope. Legacy
// list entries can still be inspected, but cannot address the chat archive.
export function supportsSessionHistory(sessionId: string): boolean {
  const id = sessionId.trim();
  if (new TextEncoder().encode(id).length > 2048 || /^mock:/i.test(id)) return false;
  const separator = id.indexOf(':');
  if (separator < 0) return false;
  const prefix = id.slice(0, separator);
  const rest = id.slice(separator + 1);
  if (prefix === 'group' || prefix === 'private') return Boolean(rest.trim());
  if (!prefix.trim()) return false;
  const kindSeparator = rest.indexOf(':');
  if (kindSeparator < 0) return false;
  const kind = rest.slice(0, kindSeparator);
  return (kind === 'group' || kind === 'private') && Boolean(rest.slice(kindSeparator + 1).trim());
}

// Calendar dates describe the browser's local day, including DST boundaries.
export function historyDateRange(start: string, end: string): { after: string; before: string } {
  const date = (value: string, nextDay = false) => {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) throw new Error('请选择有效日期');
    const [year, month, day] = value.split('-').map(Number);
    const result = new Date(0);
    result.setFullYear(year, month - 1, day);
    result.setHours(0, 0, 0, 0);
    if (result.getFullYear() !== year || result.getMonth() !== month - 1 || result.getDate() !== day) {
      throw new Error('请选择有效日期');
    }
    if (nextDay) result.setDate(result.getDate() + 1);
    return result.toISOString();
  };
  if (start && end && start > end) throw new Error('开始日期不能晚于结束日期');
  return { after: start ? date(start) : '', before: end ? date(end, true) : '' };
}

export function openSessionHistoryDialog(
  api: Pick<ReturnType<typeof createInstanceAPI>, 'getSessionHistory'>,
  sessionId: string,
  botName = DEFAULT_BOT_NAME,
): () => void {
  let closeDialog = () => {};
  let closed = false;
  let requestSeq = 0;
  let requestController: AbortController | undefined;
  const tokens = new PageTokenStack();
  let range = { after: '', before: '' };
  let loading = false;

  void openDialog({
    title: '历史记录',
    description: sessionId,
    maxWidth: '64rem',
    bodyHtml: `
      <div class="prompt-inspector-card">
        <form id="history-filter" class="flex items-end gap-2 flex-wrap">
          <div class="form-group mb-0"><label class="form-label" for="history-start">开始日期</label>
            <input class="input text-xs" type="date" id="history-start" /></div>
          <div class="form-group mb-0"><label class="form-label" for="history-end">结束日期</label>
            <input class="input text-xs" type="date" id="history-end" /></div>
          <button class="btn btn-primary btn-sm" type="submit">${icon('search', 'w-3.5 h-3.5')}<span>检索</span></button>
          <button class="btn btn-outline btn-sm" type="button" id="history-clear">全部日期</button>
        </form>
        <p class="text-xs text-muted">按本地日期检索，包含结束日期；留空可查看全部已保存记录。记录按时间倒序排列。</p>
        <p class="text-xs text-muted">仅展示已落库的聊天原文；未开启采集期间的消息及模拟聊天不会出现在这里。</p>
        <p id="history-status" class="text-xs text-muted" role="status" aria-live="polite"></p>
        <div class="prompt-section-body" id="history-records" aria-busy="true"></div>
      </div>`,
    footerHtml: `
      <div class="flex items-center justify-between gap-2 flex-wrap w-full">
        <span id="history-page" class="text-xs text-muted"></span>
        <div class="flex items-center gap-2">
          <button class="btn btn-outline btn-sm" id="history-prev" disabled>上一页</button>
          <button class="btn btn-outline btn-sm" id="history-next" disabled>下一页</button>
          <button class="btn btn-outline btn-sm dialog-close-btn">关闭</button>
        </div>
      </div>`,
    onMount(dialog, close) {
      dialog.classList.add('session-history-dialog');
      closeDialog = () => close();
      const start = dialog.querySelector<HTMLInputElement>('#history-start')!;
      const end = dialog.querySelector<HTMLInputElement>('#history-end')!;
      const status = dialog.querySelector<HTMLElement>('#history-status')!;
      const records = dialog.querySelector<HTMLElement>('#history-records')!;
      const page = dialog.querySelector<HTMLElement>('#history-page')!;
      const prev = dialog.querySelector<HTMLButtonElement>('#history-prev')!;
      const next = dialog.querySelector<HTMLButtonElement>('#history-next')!;

      const updatePagination = () => {
        prev.disabled = loading || !tokens.canGoBack;
        next.disabled = loading || !tokens.canGoNext;
        page.textContent = `第 ${tokens.pageIndex + 1} 页`;
      };

      async function load() {
        if (closed) return;
        const seq = ++requestSeq;
        requestController?.abort();
        requestController = new AbortController();
        loading = true;
        status.textContent = '正在加载历史记录…';
        records.setAttribute('aria-busy', 'true');
        records.innerHTML = '<div class="text-center text-muted p-6"><span class="spinner"></span></div>';
        updatePagination();
        try {
          const result = await api.getSessionHistory(sessionId, range.after, range.before, tokens.currentToken, requestController.signal);
          if (closed || seq !== requestSeq) return;
          tokens.setNextToken(result.nextPageToken);
          status.textContent = result.records.length ? `本页 ${result.records.length} 条记录` : '当前日期范围内暂无已保存的聊天记录';
          // Archive content is opaque, including delivered text that resembles
          // internal tool notes. Never parse it as a tool timeline annotation.
          records.innerHTML = result.records.length
            ? `<div class="group-messages-container">${result.records.map(record => `
                <div>
                  ${renderSingleMessageRow({
                    id: record.messageId || record.id,
                    role: record.role === 'assistant' ? 'assistant' : 'user',
                    sender: record.senderName,
                    senderId: record.senderId,
                    time: formatDateTime(record.occurredAt),
                    content: record.content,
                    rawText: record.content,
                  }, false, undefined, botName, false)}
                  ${record.replyTo ? `<p class="text-[11px] text-muted mt-1">回复 #${escapeHtml(record.replyTo)}</p>` : ''}
                  ${record.deliveryStatus === 'sent' ? '<p class="text-[11px] text-muted mt-1">已写入传输连接，未经平台确认</p>' : ''}
                </div>`).join('')}</div>`
            : '<div class="text-center text-muted text-xs p-6">暂无聊天记录</div>';
          // Keep the date filters visible when opening or changing pages.
          dialog.querySelector<HTMLElement>('.dialog-body')!.scrollTop = 0;
        } catch (err) {
          if (closed || seq !== requestSeq) return;
          tokens.setNextToken('');
          status.textContent = '加载历史记录失败：' + (err instanceof Error ? err.message : String(err));
          records.innerHTML = '<button class="btn btn-outline btn-sm" id="history-retry">重试</button>';
          records.querySelector('#history-retry')!.addEventListener('click', () => void load());
        } finally {
          if (!closed && seq === requestSeq) {
            loading = false;
            records.setAttribute('aria-busy', 'false');
            updatePagination();
          }
        }
      }

      dialog.querySelector('#history-filter')!.addEventListener('submit', event => {
        event.preventDefault();
        try {
          range = historyDateRange(start.value, end.value);
        } catch (err) {
          status.textContent = err instanceof Error ? err.message : String(err);
          return;
        }
        tokens.reset();
        void load();
      });
      dialog.querySelector('#history-clear')!.addEventListener('click', () => {
        start.value = '';
        end.value = '';
        range = { after: '', before: '' };
        tokens.reset();
        void load();
      });
      prev.addEventListener('click', () => {
        if (loading || !tokens.canGoBack) return;
        tokens.prev();
        void load();
      });
      next.addEventListener('click', () => {
        if (loading || !tokens.canGoNext) return;
        tokens.next();
        void load();
      });
      void load();
    },
    onClose() {
      closed = true;
      requestSeq++;
      requestController?.abort();
    },
  });
  return () => closeDialog();
}
