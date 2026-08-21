import { api } from '../api/client';
import { LogEntry, LogLevel } from '@frostagent/proto';
import {
  escapeHtml,
  formatDateTime,
  formatLogLevel,
  logLevelBadgeClass,
  PageTokenStack,
} from '../utils/formatters';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { openDialog } from '../components/dialog';
import { confirmDialog } from '../components/confirm';
import { renderPagination, attachPaginationEvents } from '../components/pagination';

export function mountLogsPage(container: HTMLElement): () => void {
  let isUnmounted = false;
  let loading = false;
  let streaming = false;
  let entries: LogEntry[] = [];
  let streamEntries: LogEntry[] = [];
  let selectedEntry: LogEntry | null = null;
  let minLevel: LogLevel = LogLevel.UNSPECIFIED;
  let sourceFilter = '';
  let pageSize = 50;
  let total = 0;
  const tokenStack = new PageTokenStack();
  let streamAbortController: AbortController | null = null;

  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="flex items-center justify-between gap-4 flex-wrap">
        <div>
          <h1 class="page-title">日志查询</h1>
          <p class="page-description">查看运行时日志、请求体/响应体详情与实时日志流</p>
        </div>
        <div class="flex items-center gap-2">
          <button class="btn btn-outline btn-icon-sm" id="logs-refresh-btn" title="刷新">
            ${icon('refresh')}
          </button>
          <button class="btn btn-destructive btn-sm" id="logs-clear-btn">
            ${icon('delete_sweep')}
            <span>清理日志</span>
          </button>
        </div>
      </header>

      <!-- Filter Card -->
      <section class="card p-4">
        <div class="flex items-end gap-3 flex-wrap">
          <div class="form-group" style="width: 10rem;">
            <label class="form-label" for="logs-level-select">最低级别</label>
            <select id="logs-level-select" class="select" style="height: 2.25rem;">
              <option value="0">全部级别</option>
              <option value="1">Debug</option>
              <option value="2">Info</option>
              <option value="3">Warn</option>
              <option value="4">Error</option>
            </select>
          </div>

          <div class="form-group flex-1" style="min-width: 14rem;">
            <label class="form-label" for="logs-source-input">来源过滤</label>
            <input id="logs-source-input" class="input" placeholder="输入来源模块，如 bot, onebot, llm..." style="height: 2.25rem;" />
          </div>

          <button class="btn btn-outline btn-sm" id="logs-apply-filter-btn" style="height: 2.25rem;">
            ${icon('filter_alt')}
            <span>应用过滤</span>
          </button>
        </div>
      </section>

      <!-- Split Table & Detail Layout -->
      <div class="grid grid-cols-1 xl:grid-cols-3 gap-4">
        <!-- Table Column (2 cols on xl) -->
        <div class="xl:col-span-2 flex flex-col">
          <div class="card" style="padding: 0; overflow: hidden;">
            <div class="table-container">
              <table class="table">
                <thead>
                  <tr>
                    <th style="width: 9rem;">时间</th>
                    <th style="width: 5rem;">级别</th>
                    <th style="width: 7rem;">来源</th>
                    <th>摘要</th>
                  </tr>
                </thead>
                <tbody id="logs-table-body">
                  <tr>
                    <td colspan="4" class="text-center text-muted" style="padding: 2rem;">
                      <span class="spinner"></span>
                      <span style="margin-left: 0.5rem;">加载中...</span>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div id="logs-pagination-container"></div>
          </div>
        </div>

        <!-- Detail Column (1 col on xl) -->
        <div class="xl:col-span-1">
          <article class="card p-4 sticky" style="top: 1rem;">
            <div class="card-header" style="padding-bottom: 0.75rem; border-bottom: 1px solid var(--border);">
              <div class="flex items-center gap-2">
                <span class="text-primary">${icon('receipt_long')}</span>
                <h2 class="card-title text-base">日志详情</h2>
              </div>
            </div>
            <div class="card-content pt-3" id="log-detail-content">
              <p class="text-sm text-muted">选择左侧一条日志查看请求/响应体详情。</p>
            </div>
          </article>
        </div>
      </div>

      <!-- Real-time Stream Card -->
      <article class="card p-4">
        <div class="card-header flex items-center justify-between border-b pb-3">
          <div class="flex items-center gap-2">
            <span class="text-primary">${icon('sensors')}</span>
            <h2 class="card-title text-base">实时日志流</h2>
          </div>
          <button class="btn btn-outline btn-sm" id="stream-toggle-btn">
            <span id="stream-toggle-icon">${icon('play_circle')}</span>
            <span id="stream-toggle-label">开始监听</span>
          </button>
        </div>
        <div class="card-content pt-3">
          <div id="stream-entries-container" class="flex flex-col gap-2" style="max-height: 22rem; overflow-y: auto;">
            <p class="text-sm text-muted">实时流尚未启动，点击右上角“开始监听”以订阅实时日志。</p>
          </div>
        </div>
      </article>
    </div>
  `;

  // Elements
  const refreshBtn = container.querySelector<HTMLButtonElement>('#logs-refresh-btn')!;
  const clearBtn = container.querySelector<HTMLButtonElement>('#logs-clear-btn')!;
  const levelSelect = container.querySelector<HTMLSelectElement>('#logs-level-select')!;
  const sourceInput = container.querySelector<HTMLInputElement>('#logs-source-input')!;
  const applyFilterBtn = container.querySelector<HTMLButtonElement>('#logs-apply-filter-btn')!;
  const tbody = container.querySelector<HTMLElement>('#logs-table-body')!;
  const paginationContainer = container.querySelector<HTMLElement>('#logs-pagination-container')!;
  const detailContent = container.querySelector<HTMLElement>('#log-detail-content')!;
  const streamToggleBtn = container.querySelector<HTMLButtonElement>('#stream-toggle-btn')!;
  const streamToggleIcon = container.querySelector<HTMLElement>('#stream-toggle-icon')!;
  const streamToggleLabel = container.querySelector<HTMLElement>('#stream-toggle-label')!;
  const streamEntriesContainer = container.querySelector<HTMLElement>('#stream-entries-container')!;

  async function loadLogs() {
    if (isUnmounted) return;
    loading = true;
    renderTable();

    try {
      const resp = await api.listLogs(pageSize, tokenStack.currentToken, minLevel, sourceFilter);
      if (isUnmounted) return;
      entries = resp.entries || [];
      tokenStack.setNextToken(resp.pagination?.pageToken ?? '');
      total = Number(resp.pagination?.total ?? entries.length);

      if (entries.length > 0 && (!selectedEntry || !entries.some((e) => e.id === selectedEntry?.id))) {
        selectedEntry = entries[0];
      } else if (entries.length === 0) {
        selectedEntry = null;
      }
    } catch (err) {
      if (isUnmounted) return;
      toast.error('加载日志失败: ' + (err instanceof Error ? err.message : String(err)));
      entries = [];
      selectedEntry = null;
    } finally {
      if (!isUnmounted) {
        loading = false;
        renderTable();
        renderDetail();
      }
    }
  }

  function renderTable() {
    if (loading && entries.length === 0) {
      tbody.innerHTML = `
        <tr>
          <td colspan="4" class="text-center text-muted" style="padding: 2rem;">
            <span class="spinner"></span>
            <span style="margin-left: 0.5rem;">加载中...</span>
          </td>
        </tr>
      `;
      paginationContainer.innerHTML = '';
      return;
    }

    if (entries.length === 0) {
      tbody.innerHTML = `
        <tr>
          <td colspan="4" class="text-center text-muted" style="padding: 2.5rem;">
            暂无日志记录。
          </td>
        </tr>
      `;
      paginationContainer.innerHTML = '';
      return;
    }

    tbody.innerHTML = entries
      .map((entry) => {
        const isSelected = selectedEntry?.id === entry.id;
        const levelBadge = logLevelBadgeClass(entry.level);
        const levelText = formatLogLevel(entry.level);

        return `
          <tr class="cursor-pointer ${isSelected ? 'row-selected' : ''}" data-id="${escapeHtml(entry.id)}" style="${isSelected ? 'background-color: var(--muted);' : ''}">
            <td class="text-xs text-muted font-mono">${escapeHtml(formatDateTime(entry.timestamp))}</td>
            <td>
              <span class="badge ${levelBadge}">${escapeHtml(levelText)}</span>
            </td>
            <td class="text-xs font-medium">${escapeHtml(entry.source || '-')}</td>
            <td style="max-width: 20rem;">
              <button class="btn btn-ghost btn-sm text-left truncate block w-full" style="padding: 0.25rem 0.5rem;" data-action="view-summary" data-id="${escapeHtml(entry.id)}" title="${escapeHtml(entry.summary || '-')}">
                ${escapeHtml(entry.summary || '-')}
              </button>
            </td>
          </tr>
        `;
      })
      .join('');

    paginationContainer.innerHTML = renderPagination({
      total,
      pageIndex: tokenStack.pageIndex,
      pageSize,
      pageSizeOptions: [25, 50, 100, 200],
      canGoBack: tokenStack.canGoBack,
      canGoNext: tokenStack.canGoNext,
      loading,
    });

    attachPaginationEvents(paginationContainer, {
      onPageSizeChange: (newSize) => {
        pageSize = newSize;
        tokenStack.reset();
        void loadLogs();
      },
      onPrevPage: () => {
        tokenStack.prev();
        void loadLogs();
      },
      onNextPage: () => {
        tokenStack.next();
        void loadLogs();
      },
    });

    // Attach row events
    tbody.querySelectorAll<HTMLTableRowElement>('tr[data-id]').forEach((row) => {
      row.addEventListener('click', (e) => {
        const target = e.target as HTMLElement;
        if (target.closest('[data-action="view-summary"]')) return; // Handled separately
        const id = row.dataset.id;
        const entry = entries.find((e) => e.id === id);
        if (entry) {
          selectedEntry = entry;
          renderTable();
          renderDetail();
        }
      });
    });

    tbody.querySelectorAll<HTMLButtonElement>('[data-action="view-summary"]').forEach((btn) => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        const id = btn.dataset.id;
        const entry = entries.find((e) => e.id === id);
        if (entry) {
          selectedEntry = entry;
          renderTable();
          renderDetail();
          openSummaryDialog(entry);
        }
      });
    });
  }

  function renderDetail() {
    if (!selectedEntry) {
      detailContent.innerHTML = `
        <p class="text-sm text-muted">选择左侧一条日志查看请求/响应体详情。</p>
      `;
      return;
    }

    detailContent.innerHTML = `
      <div class="flex flex-col gap-3">
        <div class="flex flex-wrap items-center gap-2 text-xs">
          <span class="badge ${logLevelBadgeClass(selectedEntry.level)}">${escapeHtml(formatLogLevel(selectedEntry.level))}</span>
          <span class="font-mono text-muted">${escapeHtml(formatDateTime(selectedEntry.timestamp))}</span>
          <span class="badge badge-outline">${escapeHtml(selectedEntry.source || '-')}</span>
        </div>

        <div>
          <p class="text-xs font-semibold mb-1 text-muted">请求体 (Request Body)</p>
          <pre class="card p-2.5 bg-muted text-xs font-mono whitespace-pre-wrap select-text leading-relaxed" style="max-height: 14rem; overflow-y: auto;">${escapeHtml(selectedEntry.requestBody || '（无请求体）')}</pre>
        </div>

        <div>
          <p class="text-xs font-semibold mb-1 text-muted">响应体 (Response Body)</p>
          <pre class="card p-2.5 bg-muted text-xs font-mono whitespace-pre-wrap select-text leading-relaxed" style="max-height: 14rem; overflow-y: auto;">${escapeHtml(selectedEntry.responseBody || '（无响应体）')}</pre>
        </div>
      </div>
    `;
  }

  function openSummaryDialog(entry: LogEntry) {
    openDialog({
      title: `日志摘要 - ${entry.source || '日志'}`,
      description: `${formatLogLevel(entry.level)} · ${formatDateTime(entry.timestamp)}`,
      maxWidth: '36rem',
      bodyHtml: `
        <div class="card p-4 bg-muted text-xs leading-relaxed font-mono whitespace-pre-wrap select-text" style="max-height: 24rem; overflow-y: auto;">
          ${escapeHtml(entry.summary || '无摘要内容')}
        </div>
      `,
      footerHtml: `
        <button class="btn btn-outline dialog-close-btn">关闭</button>
      `,
      onMount: (dialogEl, close) => {
        dialogEl.querySelector('.dialog-close-btn')?.addEventListener('click', () => close());
      },
    });
  }

  // Live stream control
  async function toggleStream() {
    if (streaming) {
      stopStream();
      return;
    }

    streaming = true;
    streamToggleLabel.textContent = '停止监听';
    streamToggleIcon.innerHTML = icon('stop_circle');
    streamToggleBtn.classList.remove('btn-outline');
    streamToggleBtn.classList.add('btn-destructive');

    streamAbortController = new AbortController();

    try {
      for await (const entry of api.streamLogs(minLevel, sourceFilter, streamAbortController.signal)) {
        if (isUnmounted) break;
        streamEntries.unshift(entry);
        if (streamEntries.length > 200) streamEntries.pop();
        renderStreamEntries();
      }
    } catch (err) {
      if (!streamAbortController?.signal.aborted) {
        toast.error('实时日志流异常: ' + (err instanceof Error ? err.message : String(err)));
      }
    } finally {
      stopStream();
    }
  }

  function stopStream() {
    if (streamAbortController) {
      streamAbortController.abort();
      streamAbortController = null;
    }
    streaming = false;
    streamToggleLabel.textContent = '开始监听';
    streamToggleIcon.innerHTML = icon('play_circle');
    streamToggleBtn.classList.remove('btn-destructive');
    streamToggleBtn.classList.add('btn-outline');
  }

  function renderStreamEntries() {
    if (streamEntries.length === 0) {
      streamEntriesContainer.innerHTML = `
        <p class="text-sm text-muted">实时流正在监听中，等待新日志到达...</p>
      `;
      return;
    }

    streamEntriesContainer.innerHTML = streamEntries
      .slice(0, 50)
      .map(
        (e) => `
        <div class="card p-2.5 bg-muted text-xs flex flex-col gap-1">
          <div class="flex flex-wrap items-center gap-2">
            <span class="text-muted font-mono">${escapeHtml(formatDateTime(e.timestamp))}</span>
            <span class="badge ${logLevelBadgeClass(e.level)}">${escapeHtml(formatLogLevel(e.level))}</span>
            <span class="font-medium">${escapeHtml(e.source)}</span>
          </div>
          <div class="font-mono whitespace-pre-wrap select-text mt-0.5">${escapeHtml(e.summary)}</div>
        </div>
      `,
      )
      .join('');
  }

  // Clear logs
  async function clearLogs() {
    const confirmed = await confirmDialog({
      title: '清理日志',
      message: '确认清理当前内存日志缓冲区吗？此操作无法撤销。',
      confirmLabel: '清理',
      destructive: true,
    });
    if (confirmed) {
      try {
        const success = await api.clearLogs();
        if (success) {
          toast.success('日志已清理');
          entries = [];
          streamEntries = [];
          selectedEntry = null;
          renderStreamEntries();
          tokenStack.reset();
          void loadLogs();
        }
      } catch (err) {
        toast.error('清理日志失败: ' + (err instanceof Error ? err.message : String(err)));
      }
    }
  }

  // Event handlers
  refreshBtn.addEventListener('click', () => {
    tokenStack.reset();
    void loadLogs();
  });

  clearBtn.addEventListener('click', () => void clearLogs());

  levelSelect.addEventListener('change', () => {
    minLevel = parseInt(levelSelect.value, 10) as LogLevel;
    tokenStack.reset();
    void loadLogs();
  });

  sourceInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      sourceFilter = sourceInput.value.trim();
      tokenStack.reset();
      void loadLogs();
    }
  });

  applyFilterBtn.addEventListener('click', () => {
    sourceFilter = sourceInput.value.trim();
    tokenStack.reset();
    void loadLogs();
  });

  streamToggleBtn.addEventListener('click', () => void toggleStream());

  void loadLogs();

  return () => {
    isUnmounted = true;
    stopStream();
  };
}
