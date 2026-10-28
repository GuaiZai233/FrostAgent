import { instanceURL, instanceState } from '../instance-state';
import { createInstanceAPI } from '../api/client';
import type { StickerItem, GetStickerStatsResponse } from '@frostagent/proto';
import { escapeHtml } from '../utils/formatters';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { openDialog } from '../components/dialog';
import { confirmDialog } from '../components/confirm';
import { StickerFeed, type StickerFeedChange } from './sticker-feed';

function formatTimestamp(
  ts: bigint | number | string | undefined | null,
): string {
  if (!ts) return '-';
  const ms = typeof ts === 'string' ? Number(ts) : Number(ts);
  if (ms <= 0 || Number.isNaN(ms)) return '-';
  const d = new Date(ms * 1000);
  return new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(d);
}

export function mountStickersPage(
  container: HTMLElement,
  api = createInstanceAPI(),
): () => void {
  let isUnmounted = false;
  let stats: GetStickerStatsResponse | null = null;
  const batchSize = 24;
  let statusFilter = '';
  let searchQuery = '';
  let sortBy = 'created_at';
  let descending = false;
  let latestStatsRequest = 0;
  const lifecycle = new AbortController();

  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="flex items-center justify-between gap-4 flex-wrap pb-1">
        <div>
          <h1 class="page-title">表情包摘取</h1>
        </div>
        <div class="flex items-center gap-2 flex-wrap">
          <a class="btn btn-outline" href="/api/instances/${instanceState.selected!.id}/backup/stickers" download="stickers.zip">下载贴图备份</a>
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

          <select id="sticker-sort-select" class="select text-xs" style="width: 12rem;" title="召回次数使用现有收集权重">
            <option value="created_at">按添加时间排序</option>
            <option value="weight">按召回次数排序（收集权重）</option>
          </select>

          <select id="sticker-sort-order-select" class="select text-xs" style="width: 5rem;" title="排序方向">
            <option value="asc">顺序</option>
            <option value="desc">倒序</option>
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

      <div id="sticker-load-more" class="flex items-center justify-center gap-3 flex-wrap text-sm text-muted p-3" role="status" aria-live="polite"></div>
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

  const searchInput = container.querySelector<HTMLInputElement>(
    '#sticker-search-input',
  )!;
  const searchBtn = container.querySelector<HTMLButtonElement>(
    '#sticker-search-btn',
  )!;
  const statusSelect = container.querySelector<HTMLSelectElement>(
    '#sticker-status-select',
  )!;
  const sortSelect = container.querySelector<HTMLSelectElement>(
    '#sticker-sort-select',
  )!;
  const sortOrderSelect = container.querySelector<HTMLSelectElement>(
    '#sticker-sort-order-select',
  )!;
  const retryBtn =
    container.querySelector<HTMLButtonElement>('#sticker-retry-btn')!;
  const retryIcon = container.querySelector<HTMLElement>(
    '#sticker-retry-icon',
  )!;
  const refreshBtn = container.querySelector<HTMLButtonElement>(
    '#sticker-refresh-btn',
  )!;
  const uploadInput = container.querySelector<HTMLInputElement>(
    '#sticker-upload-input',
  )!;

  const statTotal = container.querySelector<HTMLElement>(
    '#stat-sticker-total',
  )!;
  const statReady = container.querySelector<HTMLElement>(
    '#stat-sticker-ready',
  )!;
  const statUnsummarized = container.querySelector<HTMLElement>(
    '#stat-sticker-unsummarized',
  )!;

  const grid = container.querySelector<HTMLElement>('#sticker-grid')!;
  const loadMoreContainer =
    container.querySelector<HTMLElement>('#sticker-load-more')!;
  const feed = new StickerFeed(
    (token) =>
      api.listStickers(
        batchSize,
        token,
        statusFilter,
        searchQuery,
        sortBy,
        descending,
      ),
    renderFeed,
  );
  const observer =
    typeof IntersectionObserver === 'undefined'
      ? null
      : new IntersectionObserver(
          (entries) => {
            if (entries.some((entry) => entry.isIntersecting) && !feed.error) {
              void feed.loadMore();
            }
          },
          { rootMargin: '120px' },
        );

  async function loadStats() {
    if (isUnmounted) return;
    const requestId = ++latestStatsRequest;
    try {
      const response = await api.getStickerStats();
      if (isUnmounted || requestId !== latestStatsRequest) return;
      stats = response;
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

  function loadStickers() {
    return feed.reset();
  }

  function stickerImageUrl(id: string): string {
    return `${window.location.origin}${instanceURL(`/api/sticker/${encodeURIComponent(id)}/image`)}`;
  }

  function renderFeed(change: StickerFeedChange) {
    if (change.kind === 'reset') grid.innerHTML = '';
    if (change.kind === 'append' && change.stickers.length > 0) {
      if (feed.stickers.length === change.stickers.length) grid.innerHTML = '';
      grid.insertAdjacentHTML(
        'beforeend',
        change.stickers.map(renderSticker).join(''),
      );
    }
    if (feed.loading && feed.stickers.length === 0) {
      grid.innerHTML = `
        <div class="text-center text-muted" style="grid-column: 1 / -1; padding: 2.5rem;">
          <span class="spinner"></span>
          <span style="margin-left: 0.5rem;">加载中...</span>
        </div>
      `;
    } else if (feed.stickers.length === 0) {
      grid.innerHTML = `
        <div class="text-center text-muted" style="grid-column: 1 / -1; padding: 3rem;">
          ${feed.error ? '未能加载表情包，请在下方重试。' : '暂无表情包记录。'}
        </div>
      `;
    }

    loadMoreContainer.setAttribute('aria-busy', String(feed.loading));
    if (feed.loading) {
      loadMoreContainer.innerHTML =
        '<span class="spinner"></span><span>正在加载表情包，请稍候...</span>';
    } else if (feed.error) {
      loadMoreContainer.innerHTML = `
        <span class="text-destructive">加载失败：${escapeHtml(feed.error)}</span>
        <button class="btn btn-outline btn-sm" data-action="load-stickers">重试加载</button>
      `;
    } else if (feed.hasMore) {
      loadMoreContainer.innerHTML = `
        <span>已加载 ${feed.stickers.length} / ${feed.total} 张，向下滚动加载更多</span>
        <button class="btn btn-outline btn-sm" data-action="load-stickers">加载更多</button>
      `;
      // A short batch may leave the sentinel visible. Observe it again so the
      // viewport continues filling without requiring an impossible scroll.
      observer?.unobserve(loadMoreContainer);
      observer?.observe(loadMoreContainer);
    } else {
      loadMoreContainer.innerHTML = `<span>已加载全部 ${feed.stickers.length} 张表情包</span>`;
    }
  }

  function renderSticker(s: StickerItem): string {
    const isReady = s.status === 'ready';
    const statusBadge = isReady
      ? `<span class="badge badge-success text-[10px] px-1 py-0">已摘要</span>`
      : `<span class="badge badge-warning text-[10px] px-1 py-0">未摘要</span>`;
    const inappropriateBadge = s.suspectedInappropriate
      ? `<span class="badge badge-destructive sticker-flag-badge text-[10px] px-1 py-0">疑似不合适</span>`
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
                data-auth-src="${stickerImageUrl(s.id)}"
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
  }

  loadMoreContainer.addEventListener(
    'click',
    (event) => {
      if ((event.target as Element).closest('[data-action="load-stickers"]')) {
        void feed.loadMore();
      }
    },
    { signal: lifecycle.signal },
  );

  // Delegate clicks so appending a batch does not replace existing image nodes.
  grid.addEventListener(
    'click',
    async (event) => {
      const button = (event.target as Element).closest<HTMLButtonElement>(
        'button[data-action]',
      );
      if (!button || isUnmounted) return;
      const sticker = feed.stickers.find(
        (item) => item.id === button.dataset.id,
      );
      if (!sticker) return;
      if (button.dataset.action === 'edit-sticker') {
        openEditDialog(sticker);
        return;
      }
      if (button.dataset.action !== 'delete-sticker') return;
      const confirmed = await confirmDialog({
        title: '删除表情包',
        message: '确定要删除这个表情包吗？此操作不可逆。',
        confirmLabel: '删除',
        destructive: true,
        signal: lifecycle.signal,
      });
      if (!confirmed || isUnmounted) return;
      try {
        const res = await api.deleteSticker(sticker.id);
        if (isUnmounted) return;
        if (res.success) {
          toast.success('表情包已删除');
          void loadStats();
          void loadStickers();
        } else {
          toast.error('删除失败: ' + res.error);
        }
      } catch (err) {
        if (!isUnmounted)
          toast.error(
            '删除失败: ' + (err instanceof Error ? err.message : String(err)),
          );
      }
    },
    { signal: lifecycle.signal },
  );

  function openEditDialog(s: StickerItem) {
    let closeOnUnmount: (() => void) | undefined;
    openDialog({
      title: '编辑表情包',
      maxWidth: '32rem',
      bodyHtml: `
        <div class="flex flex-col gap-3.5">
          <div style="text-align: center;">
            <img
              data-auth-src="${stickerImageUrl(s.id)}"
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
        ${
          s.suspectedInappropriate
            ? '<button class="btn btn-destructive btn-sm" style="margin-right: auto;" id="edit-sticker-toggle-flag">移除不合适标记</button>'
            : '<button class="btn btn-primary btn-sm" style="margin-right: auto;" id="edit-sticker-toggle-flag">添加不合适标记</button>'
        }
        <button class="btn btn-outline btn-sm" id="edit-sticker-cancel">取消</button>
        <button class="btn btn-primary btn-sm" id="edit-sticker-save">保存</button>
      `,
      onClose: () => {
        if (closeOnUnmount)
          lifecycle.signal.removeEventListener('abort', closeOnUnmount);
      },
      onMount: (dialogEl, close) => {
        closeOnUnmount = () => close();
        lifecycle.signal.addEventListener('abort', closeOnUnmount, {
          once: true,
        });
        const cancelBtn = dialogEl.querySelector('#edit-sticker-cancel')!;
        const saveBtn =
          dialogEl.querySelector<HTMLButtonElement>('#edit-sticker-save')!;
        const toggleFlagBtn = dialogEl.querySelector<HTMLButtonElement>(
          '#edit-sticker-toggle-flag',
        );
        const descInput =
          dialogEl.querySelector<HTMLTextAreaElement>('#edit-sticker-desc')!;
        const keywordsInput = dialogEl.querySelector<HTMLInputElement>(
          '#edit-sticker-keywords',
        )!;

        cancelBtn.addEventListener('click', () => close());
        toggleFlagBtn?.addEventListener('click', async () => {
          if (isUnmounted) return;
          try {
            toggleFlagBtn.disabled = true;
            const res = s.suspectedInappropriate
              ? await api.clearStickerInappropriateFlag(s.id)
              : await api.markStickerInappropriateFlag(s.id);
            if (isUnmounted) return;
            if (res.success) {
              toast.success(
                s.suspectedInappropriate
                  ? '已移除疑似不合适标记'
                  : '已添加疑似不合适标记',
              );
              close();
              void loadStickers();
            } else {
              toast.error(
                `${s.suspectedInappropriate ? '移除' : '添加'}标记失败: ${res.error}`,
              );
            }
          } catch (err) {
            if (!isUnmounted)
              toast.error(
                `${s.suspectedInappropriate ? '移除' : '添加'}标记失败: ${
                  err instanceof Error ? err.message : String(err)
                }`,
              );
          } finally {
            if (!isUnmounted) toggleFlagBtn.disabled = false;
          }
        });
        saveBtn.addEventListener('click', async () => {
          if (isUnmounted) return;
          const description = descInput.value.trim();
          const keywords = keywordsInput.value
            .split(',')
            .map((k) => k.trim())
            .filter(Boolean);

          try {
            saveBtn.disabled = true;
            const res = await api.updateStickerKeywords(
              s.id,
              description,
              keywords,
            );
            if (isUnmounted) return;
            if (res.success) {
              toast.success('表情包已更新');
              close();
              void loadStickers();
            } else {
              toast.error('更新失败: ' + res.error);
            }
          } catch (err) {
            if (!isUnmounted)
              toast.error(
                '更新失败: ' +
                  (err instanceof Error ? err.message : String(err)),
              );
          } finally {
            if (!isUnmounted) saveBtn.disabled = false;
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
      if (isUnmounted) break;
      try {
        const buf = await file.arrayBuffer();
        if (isUnmounted) break;
        const res = await api.uploadSticker(new Uint8Array(buf), file.name);
        if (isUnmounted) return;
        if (res.success) {
          success++;
        } else {
          fail++;
          toast.error(`上传 ${file.name} 失败: ${res.error}`);
        }
      } catch (err) {
        if (isUnmounted) return;
        fail++;
        toast.error(
          `上传 ${file.name} 失败: ${err instanceof Error ? err.message : String(err)}`,
        );
      }
    }

    if (isUnmounted) return;
    input.value = '';
    if (success > 0) {
      toast.success(
        `已上传 ${success} 张表情包${fail ? `，${fail} 张失败` : ''}`,
      );
      void loadStats();
      void loadStickers();
    }
  }

  async function retryUnsummarized() {
    if (isUnmounted) return;
    retryBtn.disabled = true;
    retryIcon.innerHTML = `<span class="spinner inline-block" style="width: 0.875rem; height: 0.875rem;"></span>`;

    try {
      const res = await api.retryAllUnsummarized();
      if (isUnmounted) return;
      if (res.enqueuedCount > 0) {
        toast.success(`已加入摘要队列 ${res.enqueuedCount} 张`);
      } else {
        toast.warning('没有需要重试的表情包');
      }
    } catch (err) {
      if (!isUnmounted)
        toast.error(
          '重试失败: ' + (err instanceof Error ? err.message : String(err)),
        );
    } finally {
      if (!isUnmounted) {
        retryBtn.disabled = false;
        retryIcon.innerHTML = icon('refresh');
      }
    }
  }

  // Event listeners
  searchBtn.addEventListener('click', () => {
    searchQuery = searchInput.value.trim();
    void loadStickers();
  });

  searchInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      searchQuery = searchInput.value.trim();
      void loadStickers();
    }
  });

  statusSelect.addEventListener('change', () => {
    statusFilter = statusSelect.value;
    void loadStickers();
  });

  sortSelect.addEventListener('change', () => {
    sortBy = sortSelect.value;
    void loadStickers();
  });

  sortOrderSelect.addEventListener('change', () => {
    descending = sortOrderSelect.value === 'desc';
    void loadStickers();
  });

  retryBtn.addEventListener('click', retryUnsummarized);
  uploadInput.addEventListener('change', handleUpload);

  refreshBtn.addEventListener('click', () => {
    statusFilter = '';
    statusSelect.value = '';
    searchQuery = '';
    searchInput.value = '';
    sortBy = 'created_at';
    sortSelect.value = sortBy;
    descending = false;
    sortOrderSelect.value = 'asc';
    void loadStats();
    void loadStickers();
  });

  // Initial load
  void loadStats();
  void loadStickers();

  return () => {
    isUnmounted = true;
    lifecycle.abort();
    observer?.disconnect();
    feed.dispose();
  };
}
