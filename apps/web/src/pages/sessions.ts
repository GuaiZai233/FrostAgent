import { api } from '../api/client';
import { SessionInfo } from '@frostagent/proto';
import {
  escapeHtml,
  formatCount,
  formatDateTime,
  formatPlatform,
  isGroupSession,
  PageTokenStack,
} from '../utils/formatters';
import { icon } from '../components/icons';
import { renderPagination, attachPaginationEvents } from '../components/pagination';
import { openDialog } from '../components/dialog';
import { confirmDialog } from '../components/confirm';
import { toast } from '../components/toast';

export function mountSessionsPage(container: HTMLElement): () => void {
  let isUnmounted = false;
  let loading = false;
  let sessions: SessionInfo[] = [];
  let totalSessions = 0;
  let pageSize = 20;
  const tokenStack = new PageTokenStack();

  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="flex items-center justify-between gap-4 flex-wrap pb-1">
        <div>
          <h1 class="page-title">会话管理</h1>
          <p class="page-description">查看与管理 Bot 参与的所有私聊及群聊会话状态</p>
        </div>
        <button class="btn btn-outline btn-sm" id="sessions-refresh-btn">
          ${icon('refresh', 'w-3.5 h-3.5')}
          <span>刷新</span>
        </button>
      </header>

      <div class="card table-card overflow-hidden">
        <div class="table-container">
          <table class="table">
            <thead>
              <tr>
                <th>会话 ID</th>
                <th style="width: 7rem;">平台</th>
                <th style="width: 6rem;">消息数</th>
                <th style="width: 9.5rem;">创建时间</th>
                <th style="width: 9.5rem;">最后活跃</th>
                <th style="width: 8rem; text-align: right;">操作</th>
              </tr>
            </thead>
            <tbody id="sessions-table-body">
              <tr>
                <td colspan="6" class="text-center text-muted" style="padding: 2.5rem;">
                  <span class="spinner"></span>
                  <span style="margin-left: 0.5rem;">加载中...</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div id="sessions-pagination-container"></div>
      </div>
    </div>
  `;

  const tbody = container.querySelector<HTMLElement>('#sessions-table-body')!;
  const paginationContainer = container.querySelector<HTMLElement>('#sessions-pagination-container')!;
  const refreshBtn = container.querySelector<HTMLButtonElement>('#sessions-refresh-btn')!;

  async function loadSessions() {
    if (isUnmounted) return;
    loading = true;
    render();

    try {
      const res = await api.getSessions(pageSize, tokenStack.currentToken);

      if (isUnmounted) return;
      sessions = res.sessions;
      tokenStack.setNextToken(res.pagination?.pageToken);
      totalSessions = Number(res.pagination?.total ?? res.sessions.length);
    } catch (err) {
      if (isUnmounted) return;
      toast.error('加载会话列表失败: ' + (err instanceof Error ? err.message : String(err)));
      sessions = [];
    } finally {
      if (!isUnmounted) {
        loading = false;
        render();
      }
    }
  }

  function render() {
    if (loading && sessions.length === 0) {
      tbody.innerHTML = `
        <tr>
          <td colspan="6" class="text-center text-muted" style="padding: 2.5rem;">
            <span class="spinner"></span>
            <span style="margin-left: 0.5rem;">加载中...</span>
          </td>
        </tr>
      `;
      paginationContainer.innerHTML = '';
      return;
    }

    if (sessions.length === 0) {
      tbody.innerHTML = `
        <tr>
          <td colspan="6" class="text-center text-muted" style="padding: 3rem;">
            暂无会话记录。
          </td>
        </tr>
      `;
      paginationContainer.innerHTML = '';
      return;
    }

    tbody.innerHTML = sessions
      .map((session) => {
        const isGroup = isGroupSession(session);
        const hasSummary = Boolean(session.groupSummary);

        const idContent = isGroup
          ? `
            <button class="btn btn-ghost btn-sm text-left flex items-center gap-1.5 p-0 font-normal hover:text-primary" data-action="view-summary" data-id="${escapeHtml(
              session.sessionId,
            )}">
              <span class="font-mono text-xs font-medium">${escapeHtml(session.sessionId)}</span>
              ${
                hasSummary
                  ? `<span class="badge badge-secondary text-[11px] px-1.5 py-0">${icon(
                      'sparkles',
                      'w-3 h-3 text-primary',
                    )} 总结</span>`
                  : ''
              }
            </button>
          `
          : `<span class="font-mono text-xs">${escapeHtml(session.sessionId)}</span>`;

        const deleteSummaryBtn =
          isGroup && hasSummary
            ? `
              <button class="btn btn-ghost btn-icon-sm text-destructive" style="width: 1.75rem; height: 1.75rem;" title="删除群聊总结" data-action="delete-summary" data-id="${escapeHtml(
                session.sessionId,
              )}">
                ${icon('trash', 'w-3.5 h-3.5')}
              </button>
            `
            : '';

        const groupId = isGroup ? groupIdFromSessionId(session.sessionId) : '';
        const modelRouterBtn =
          isGroup && groupId
            ? `
              <a class="btn btn-ghost btn-icon-sm" style="width: 1.75rem; height: 1.75rem;" title="配置群模型" href="#/settings/model-router?platform=${encodeURIComponent(
                session.platform || 'onebot',
              )}&group_id=${encodeURIComponent(groupId)}">
                ${icon('settings', 'w-3.5 h-3.5')}
              </a>
            `
            : '';

        return `
          <tr>
            <td>${idContent}</td>
            <td><span class="badge badge-outline text-xs">${escapeHtml(formatPlatform(session.platform))}</span></td>
            <td><span class="font-mono text-xs">${escapeHtml(formatCount(session.messageCount))}</span></td>
            <td class="text-xs text-muted font-mono">${escapeHtml(formatDateTime(session.createdAt))}</td>
            <td class="text-xs text-muted font-mono">${escapeHtml(formatDateTime(session.lastActive))}</td>
            <td><div class="flex items-center justify-end gap-1">${modelRouterBtn}${deleteSummaryBtn}</div></td>
          </tr>
        `;
      })
      .join('');

    paginationContainer.innerHTML = renderPagination({
      total: totalSessions,
      pageIndex: tokenStack.pageIndex,
      pageSize,
      canGoBack: tokenStack.canGoBack,
      canGoNext: tokenStack.canGoNext,
      loading,
    });

    attachPaginationEvents(paginationContainer, {
      onPageSizeChange: (newSize) => {
        pageSize = newSize;
        tokenStack.reset();
        void loadSessions();
      },
      onPrevPage: () => {
        tokenStack.prev();
        void loadSessions();
      },
      onNextPage: () => {
        tokenStack.next();
        void loadSessions();
      },
    });

    // Attach row events
    tbody.querySelectorAll<HTMLButtonElement>('[data-action="view-summary"]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const id = btn.dataset.id;
        const session = sessions.find((s) => s.sessionId === id);
        if (session) {
          showSummaryDialog(session);
        }
      });
    });

    tbody.querySelectorAll<HTMLButtonElement>('[data-action="delete-summary"]').forEach((btn) => {
      btn.addEventListener('click', async () => {
        const id = btn.dataset.id;
        if (!id) return;
        const confirmed = await confirmDialog({
          title: '删除群聊总结',
          message: `确定要清空会话 ${id} 的群聊总结吗？此操作不可逆。`,
          confirmLabel: '删除',
          destructive: true,
        });

        if (confirmed) {
          try {
            await api.deleteGroupSummary(id);
            toast.success('群聊总结已清空');
            void loadSessions();
          } catch (err) {
            toast.error('删除总结失败: ' + (err instanceof Error ? err.message : String(err)));
          }
        }
      });
    });
  }

  function showSummaryDialog(session: SessionInfo) {
    const summary = session.groupSummary || '暂无群聊总结内容。';

    openDialog({
      title: `群聊总结 - ${session.sessionId}`,
      description: `平台: ${formatPlatform(session.platform)} · 消息数: ${formatCount(session.messageCount)}`,
      maxWidth: '36rem',
      bodyHtml: `
        <div class="card p-3.5 bg-muted text-xs leading-relaxed font-mono whitespace-pre-wrap select-text text-foreground" style="max-height: 20rem; overflow-y: auto;">${escapeHtml(summary)}</div>
      `,
      footerHtml: `
        <button class="btn btn-outline btn-sm dialog-close-btn">关闭</button>
      `,
      onMount: (dialogEl, close) => {
        dialogEl.querySelector('.dialog-close-btn')?.addEventListener('click', () => close());
      },
    });
  }

  refreshBtn.addEventListener('click', () => {
    tokenStack.reset();
    void loadSessions();
  });

  void loadSessions();

  return () => {
    isUnmounted = true;
  };
}

function groupIdFromSessionId(sessionId: string): string {
  const lower = sessionId.toLowerCase();
  if (lower.startsWith('group:')) return sessionId.slice('group:'.length);
  const marker = ':group:';
  const index = lower.lastIndexOf(marker);
  return index >= 0 ? sessionId.slice(index + marker.length) : '';
}
