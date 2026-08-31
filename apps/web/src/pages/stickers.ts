import { api } from '../api/client';
import type { StickerItem, GetStickerStatsResponse } from '@frostagent/proto';
import { escapeHtml, PageTokenStack } from '../utils/formatters';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { openDialog } from '../components/dialog';
import { confirmDialog } from '../components/confirm';
import { renderPagination, attachPaginationEvents } from '../components/pagination';

function formatTimestamp(ts: bigint | number | string | undefined | null): string {
  if (!ts) return '-';
  const ms = typeof ts === 'string' ? Number(ts) : Number(ts);
  if (ms <= 0 || Number.isNaN(ms)) return '-';
  const d = new Date(ms * 1000);
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(d);
}

export function mountStickersPage(container: HTMLElement): () => void {
  let isUnmounted = false;
  let loading = false;
  let stickers: StickerItem[] = [];
  let stats: GetStickerStatsResponse | null = null;
  let total = 0;
  let pageSize = 24;
  let statusFilter = '';
  let searchQuery = '';
  const tokenStack = new PageTokenStack();

  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="flex items-center justify-between gap-4 flex-wrap pb-1">
        <div>
          <h1 class="page-title">表情包摘取</h1>
          <p class="page-description">管理 Bot 自动收集与手动上传的表情包</p>
        </div>
        <div class="flex items-center gap-2 flex-wrap">
          <div class="flex items-center gap-1" style="width: 13rem;">
            <input
              type="search"
              id="sticker-search-input"
              class="input text-xs"
              placeholder="搜索描述或关键词..."
            />
            <button class="btn btn-outline btn-icon-sm" id="sticker-search-btn" title="搜索">
              ${icon('search')}
            </button>
          </div>

          <select id="sticker-status-select" class="select text-xs" style="width: 7.5rem;" title="状态筛选">
            <option value="">全部状态</option>
            <option value="ready">已摘要</option>
            <option value="unsummarized">未摘要</option>
          </select>

          <button class="btn btn-outline" id="sticker-retry-btn" title="重新摘要所有未完成的表情包">
            <span id="sticker-retry-icon" class="inline-flex">${icon('refresh')}</span>
            <span>重试摘要</span>
          </button>

          <button class="btn btn-outline" id="sticker-refresh-btn" title="刷新列表">
            ${icon('refresh')}
          </button>

          <label class="btn btn-primary" style="cursor: pointer;">
            ${icon('upload')}
            <span>上传表情包</span>
            <input type="file" id="sticker-upload-input" accept="image/*" style="display: none;" multiple />
          </label>
        </div>
      </header>

      <!-- Stats Cards -->
      <section id="sticker-stats-container" class="grid grid-cols-3 gap-3">
        <article class="card p-3 flex flex-col gap-1">
          <p class="text-xs text-muted font-medium">总数</p>
          <p class="text-xl font-bold tracking-tight text-foreground" id="stat-sticker-total">-</p>
        </article>
        <article class="card p-3 flex flex-col gap-1">
          <p class="text-xs text-muted font-medium">已摘要</p>
          <p class="text-xl font-bold tracking-tight text-foreground" id="stat-sticker-ready">-</p>
        </article>
        <article class="card p-3 flex flex-col gap-1">
          <p class="text-xs text-muted font-medium">未摘要</p>
          <p class="text-xl font-bold tracking-tight text-foreground" id="stat-sticker-unsummarized">-</p>
        </article>
      </section>

      <!-- Sticker Grid -->
      <div id="sticker-grid-container" class="card" style="padding: 1rem;">
        <div id="sticker-grid" class="sticker-grid">
          <div class="text-center text-muted" style="grid-column: 1 / -1; padding: 2.5rem;">
            <span class="spinner"></span>
            <span style="margin-left: 0.5rem;">加载中...</span>
          </div>
        </div>
      </div>

      <div id="sticker-pagination-container"></div>
    </div>

    <style>
      .sticker-grid {
        display: grid;
        grid-template-columns: repeat(auto-fill, minmax(10rem, 1fr));
        gap: 0.75rem;
      }
      .sticker-card {
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        overflow: hidden;
        display: flex;
        flex-direction: column;
        background: var(--card);
        transition: box-shadow 0.15s, border-color 0.15s;
      }
      .sticker-card:hover {
        border-color: var(--primary);
        box-shadow: 0 2px 8px rgba(0,0,0,0.08);
      }
      .sticker-thumb-wrap {
        position: relative;
        width: 100%;
        aspect-ratio: 1;
        display: flex;
        align-items: center;
        justify-content: center;
        background: var(--muted);
        overflow: hidden;
      }
      .sticker-thumb-wrap img {
        max-width: 100%;
        max-height: 100%;
        object-fit: contain;
      }
      .sticker-status-badges {
        position: absolute;
        top: 0.25rem;
        right: 0.25rem;
        display: flex;
        flex-wrap: wrap;
        justify-content: flex-end;
        gap: 0.2rem;
        max-width: calc(100% - 0.5rem);
      }
      .sticker-flag-badge {
        background-color: var(--destructive);
        border-color: var(--destructive);
        color: var(--destructive-foreground);
        cursor: pointer;
      }
      .sticker-card-body {
        padding: 0.5rem;
        display: flex;
        flex-direction: column;
        gap: 0.25rem;
        flex: 1;
      }
      .sticker-desc {
        font-size: 0.75rem;
        line-height: 1.3;
        color: var(--foreground);
        display: -webkit-box;
        -webkit-line-clamp: 2;
        -webkit-box-orient: vertical;
        overflow: hidden;
      }
      .sticker-keywords {
        display: flex;
        flex-wrap: wrap;
        gap: 0.2rem;
      }
      .sticker-card-footer {
        padding: 0.25rem 0.5rem;
        border-top: 1px solid var(--border);
        display: flex;
        align-items: center;
        justify-content: space-between;
      }
    </style>
  `;

  const searchInput = container.querySelector<HTMLInputElement>('#sticker-search-input')!;
  const searchBtn = container.querySelector<HTMLButtonElement>('#sticker-search-btn')!;
  const statusSelect = container.querySelector<HTMLSelectElement>('#sticker-status-select')!;
  const retryBtn = container.querySelector<HTMLButtonElement>('#sticker-retry-btn')!;
  const retryIcon = container.querySelector<HTMLElement>('#sticker-retry-icon')!;
  const refreshBtn = container.querySelector<HTMLButtonElement>('#sticker-refresh-btn')!;
  const uploadInput = container.querySelector<HTMLInputElement>('#sticker-upload-input')!;

  const statTotal = container.querySelector<HTMLElement>('#stat-sticker-total')!;
  const statReady = container.querySelector<HTMLElement>('#stat-sticker-ready')!;
  const statUnsummarized = container.querySelector<HTMLElement>('#stat-sticker-unsummarized')!;

  const grid = container.querySelector<HTMLElement>('#sticker-grid')!;
  const paginationContainer = container.querySelector<HTMLElement>('#sticker-pagination-container')!;

  async function loadStats() {
    try {
      stats = await api.getStickerStats();
      renderStats();
    } catch {
      // non-critical
    }
  }

  function renderStats() {
    if (!stats) return;
    statTotal.textContent = String(stats.total);
    statReady.textContent = String(stats.ready);
    statUnsummarized.textContent = String(stats.unsummarized);
  }

  async function loadStickers() {
    if (isUnmounted) return;
    loading = true;
    renderGrid();

    try {
      const res = await api.listStickers(pageSize, tokenStack.currentToken, statusFilter, searchQuery);
      if (isUnmounted) return;
      stickers = res.stickers;
      tokenStack.setNextToken(res.nextPageToken ?? '');
      total = res.totalCount;
    } catch (err) {
      if (isUnmounted) return;
      toast.error('加载表情包失败: ' + (err instanceof Error ? err.message : String(err)));
      stickers = [];
    } finally {
      if (!isUnmounted) {
        loading = false;
        renderGrid();
      }
    }
  }

  function stickerImageUrl(id: string): string {
    return `${window.location.origin}/api/sticker/${encodeURIComponent(id)}/image`;
  }

  function renderGrid() {
    if (loading && stickers.length === 0) {
      grid.innerHTML = `
        <div class="text-center text-muted" style="grid-column: 1 / -1; padding: 2.5rem;">
          <span class="spinner"></span>
          <span style="margin-left: 0.5rem;">加载中...</span>
        </div>
      `;
      paginationContainer.innerHTML = '';
      return;
    }

    if (stickers.length === 0) {
      grid.innerHTML = `
        <div class="text-center text-muted" style="grid-column: 1 / -1; padding: 3rem;">
          暂无表情包记录。
        </div>
      `;
      paginationContainer.innerHTML = '';
      return;
    }

    grid.innerHTML = stickers
      .map((s) => {
        const isReady = s.status === 'ready';
        const statusBadge = isReady
          ? `<span class="badge badge-success text-[10px] px-1 py-0">已摘要</span>`
          : `<span class="badge badge-warning text-[10px] px-1 py-0">未摘要</span>`;
        const inappropriateBadge = s.suspectedInappropriate
          ? `<button type="button" class="badge badge-destructive sticker-flag-badge text-[10px] px-1 py-0" data-action="clear-inappropriate-flag" data-id="${escapeHtml(s.id)}" title="移除疑似不合适标记">疑似不合适</button>`
          : '';

        const keywordsHtml = (s.keywords || [])
          .slice(0, 4)
          .map(
            (k) =>
              `<span class="badge badge-secondary text-[10px] px-1 py-0">${escapeHtml(k)}</span>`,
          )
          .join('');
        const moreCount = (s.keywords || []).length - 4;
        const moreHtml =
          moreCount > 0
            ? `<span class="badge badge-outline text-[10px] px-1 py-0 text-muted">+${moreCount}</span>`
            : '';

        return `
          <div class="sticker-card" data-id="${escapeHtml(s.id)}">
            <div class="sticker-thumb-wrap">
              <img
                src="${stickerImageUrl(s.id)}"
                alt="${escapeHtml(s.description || '表情包')}"
                loading="lazy"
              />
              <div class="sticker-status-badges">${inappropriateBadge}${statusBadge}</div>
            </div>
            <div class="sticker-card-body">
              <div class="sticker-desc" title="${escapeHtml(s.description || '')}">
                ${escapeHtml(s.description || '暂无描述')}
              </div>
              <div class="sticker-keywords">${keywordsHtml}${moreHtml}</div>
            </div>
            <div class="sticker-card-footer">
              <span class="text-[10px] text-muted font-mono">W:${s.weight}</span>
              <div class="flex items-center gap-0.5">
                <button class="btn btn-ghost btn-icon-sm" style="width: 1.5rem; height: 1.5rem;" data-action="edit-sticker" data-id="${escapeHtml(s.id)}" title="编辑">
                  ${icon('pencil', 'w-3 h-3')}
                </button>
                <button class="btn btn-ghost btn-icon-sm text-destructive" style="width: 1.5rem; height: 1.5rem;" data-action="delete-sticker" data-id="${escapeHtml(s.id)}" title="删除">
                  ${icon('trash', 'w-3 h-3')}
                </button>
              </div>
            </div>
          </div>
        `;
      })
      .join('');

    paginationContainer.innerHTML = renderPagination({
      total,
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
        void loadStickers();
      },
      onPrevPage: () => {
        tokenStack.prev();
        void loadStickers();
      },
      onNextPage: () => {
        tokenStack.next();
        void loadStickers();
      },
    });

    grid
      .querySelectorAll<HTMLButtonElement>('[data-action="clear-inappropriate-flag"]')
      .forEach((btn) => {
        btn.addEventListener('click', async (e) => {
          e.stopPropagation();
          const id = btn.dataset.id;
          if (!id) return;
          const confirmed = await confirmDialog({
            title: '移除疑似不合适标记',
            message: '移除后，这个表情包会恢复为可检索和可发送状态。确定继续吗？',
            confirmLabel: '移除标记',
          });
          if (!confirmed) return;

          try {
            const res = await api.clearStickerInappropriateFlag(id);
            if (res.success) {
              toast.success('已移除疑似不合适标记');
              void loadStickers();
            } else {
              toast.error('移除标记失败: ' + res.error);
            }
          } catch (err) {
            toast.error('移除标记失败: ' + (err instanceof Error ? err.message : String(err)));
          }
        });
      });

    // Edit button events
    grid.querySelectorAll<HTMLButtonElement>('[data-action="edit-sticker"]').forEach((btn) => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        const id = btn.dataset.id;
        const s = stickers.find((st) => st.id === id);
        if (s) openEditDialog(s);
      });
    });

    // Delete button events
    grid.querySelectorAll<HTMLButtonElement>('[data-action="delete-sticker"]').forEach((btn) => {
      btn.addEventListener('click', async (e) => {
        e.stopPropagation();
        const id = btn.dataset.id;
        if (!id) return;
        const confirmed = await confirmDialog({
          title: '删除表情包',
          message: '确定要删除这个表情包吗？此操作不可逆。',
          confirmLabel: '删除',
          destructive: true,
        });
        if (confirmed) {
          try {
            const res = await api.deleteSticker(id);
            if (res.success) {
              toast.success('表情包已删除');
              void loadStats();
              void loadStickers();
            } else {
              toast.error('删除失败: ' + res.error);
            }
          } catch (err) {
            toast.error('删除失败: ' + (err instanceof Error ? err.message : String(err)));
          }
        }
      });
    });
  }

  function openEditDialog(s: StickerItem) {
    openDialog({
      title: '编辑表情包',
      maxWidth: '32rem',
      bodyHtml: `
        <div class="flex flex-col gap-3.5">
          <div style="text-align: center;">
            <img
              src="${stickerImageUrl(s.id)}"
              alt="表情包预览"
              style="max-width: 12rem; max-height: 12rem; border-radius: var(--radius-md); border: 1px solid var(--border);"
            />
          </div>
          <div class="grid grid-cols-2 gap-2 p-3 rounded-md border border-border bg-muted text-xs">
            <div>
              <span class="text-muted">ID:</span>
              <span class="font-mono ml-1 break-all text-foreground select-all">${escapeHtml(s.id)}</span>
            </div>
            <div>
              <span class="text-muted">权重:</span>
              <span class="ml-1 font-medium text-foreground">${s.weight}</span>
            </div>
            <div>
              <span class="text-muted">状态:</span>
              <span class="ml-1 text-foreground">${s.status === 'ready' ? '已摘要' : '未摘要'}</span>
            </div>
            <div>
              <span class="text-muted">创建时间:</span>
              <span class="ml-1 font-mono text-foreground">${escapeHtml(formatTimestamp(s.createdAt))}</span>
            </div>
          </div>
          <div class="form-group">
            <label class="form-label" for="edit-sticker-desc">描述</label>
            <textarea id="edit-sticker-desc" class="textarea text-xs" rows="3">${escapeHtml(s.description)}</textarea>
          </div>
          <div class="form-group">
            <label class="form-label" for="edit-sticker-keywords">关键词（逗号分隔）</label>
            <input id="edit-sticker-keywords" class="input text-xs" value="${escapeHtml((s.keywords || []).join(', '))}" />
          </div>
        </div>
      `,
      footerHtml: `
        <button class="btn btn-outline btn-sm" id="edit-sticker-cancel">取消</button>
        <button class="btn btn-primary btn-sm" id="edit-sticker-save">保存</button>
      `,
      onMount: (dialogEl, close) => {
        const cancelBtn = dialogEl.querySelector('#edit-sticker-cancel')!;
        const saveBtn = dialogEl.querySelector<HTMLButtonElement>('#edit-sticker-save')!;
        const descInput = dialogEl.querySelector<HTMLTextAreaElement>('#edit-sticker-desc')!;
        const keywordsInput = dialogEl.querySelector<HTMLInputElement>('#edit-sticker-keywords')!;

        cancelBtn.addEventListener('click', () => close());
        saveBtn.addEventListener('click', async () => {
          const description = descInput.value.trim();
          const keywords = keywordsInput.value
            .split(',')
            .map((k) => k.trim())
            .filter(Boolean);

          try {
            saveBtn.disabled = true;
            const res = await api.updateStickerKeywords(s.id, description, keywords);
            if (res.success) {
              toast.success('表情包已更新');
              close();
              void loadStickers();
            } else {
              toast.error('更新失败: ' + res.error);
            }
          } catch (err) {
            toast.error('更新失败: ' + (err instanceof Error ? err.message : String(err)));
          } finally {
            saveBtn.disabled = false;
          }
        });
      },
    });
  }

  async function handleUpload(e: Event) {
    const input = e.target as HTMLInputElement;
    const files = input.files;
    if (!files || files.length === 0) return;

    let success = 0;
    let fail = 0;

    for (const file of Array.from(files)) {
      try {
        const buf = await file.arrayBuffer();
        const res = await api.uploadSticker(new Uint8Array(buf), file.name);
        if (res.success) {
          success++;
        } else {
          fail++;
          toast.error(`上传 ${file.name} 失败: ${res.error}`);
        }
      } catch (err) {
        fail++;
        toast.error(`上传 ${file.name} 失败: ${err instanceof Error ? err.message : String(err)}`);
      }
    }

    input.value = '';
    if (success > 0) {
      toast.success(`已上传 ${success} 张表情包${fail ? `，${fail} 张失败` : ''}`);
      void loadStats();
      void loadStickers();
    }
  }

  async function retryUnsummarized() {
    retryBtn.disabled = true;
    retryIcon.innerHTML = `<span class="spinner inline-block" style="width: 0.875rem; height: 0.875rem;"></span>`;

    try {
      const res = await api.retryAllUnsummarized();
      if (res.enqueuedCount > 0) {
        toast.success(`已加入摘要队列 ${res.enqueuedCount} 张`);
      } else {
        toast.warning('没有需要重试的表情包');
      }
    } catch (err) {
      toast.error('重试失败: ' + (err instanceof Error ? err.message : String(err)));
    } finally {
      retryBtn.disabled = false;
      retryIcon.innerHTML = icon('refresh');
    }
  }

  // Event listeners
  searchBtn.addEventListener('click', () => {
    searchQuery = searchInput.value.trim();
    tokenStack.reset();
    void loadStickers();
  });

  searchInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      searchQuery = searchInput.value.trim();
      tokenStack.reset();
      void loadStickers();
    }
  });

  statusSelect.addEventListener('change', () => {
    statusFilter = statusSelect.value;
    tokenStack.reset();
    void loadStickers();
  });

  retryBtn.addEventListener('click', retryUnsummarized);
  uploadInput.addEventListener('change', handleUpload);

  refreshBtn.addEventListener('click', () => {
    tokenStack.reset();
    statusFilter = '';
    statusSelect.value = '';
    searchQuery = '';
    searchInput.value = '';
    void loadStats();
    void loadStickers();
  });

  // Initial load
  void loadStats();
  void loadStickers();

  return () => {
    isUnmounted = true;
  };
}
