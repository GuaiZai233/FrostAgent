import { api } from '../api/client';
import { MemoryEntry, GetMemoryStatsResponse } from '@frostagent/proto';
import { escapeHtml, formatDateTime, PageTokenStack } from '../utils/formatters';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { openDialog } from '../components/dialog';
import { confirmDialog } from '../components/confirm';
import { renderPagination, attachPaginationEvents } from '../components/pagination';

export function mountMemoryPage(container: HTMLElement): () => void {
  let isUnmounted = false;
  let loading = false;
  let reflecting = false;
  let memories: MemoryEntry[] = [];
  let stats: GetMemoryStatsResponse | null = null;
  let total = 0;
  let pageSize = 20;
  let searchQuery = '';
  let ownerFilter = '';
  const selectedIds = new Set<string>();
  const tokenStack = new PageTokenStack();

  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="flex items-center justify-between gap-4 flex-wrap pb-1">
        <div>
          <h1 class="page-title">记忆管理</h1>
          <p class="page-description">管理 Bot 长期记忆、用户专属知识与反思提炼</p>
        </div>
        <div class="flex items-center gap-2 flex-wrap">
          <div class="flex items-center gap-1" style="width: 13rem;">
            <input
              type="search"
              id="memory-search-input"
              class="input text-xs"
              placeholder="搜索记忆内容..."
            />
            <button class="btn btn-outline btn-icon-sm" id="memory-search-btn" title="搜索">
              ${icon('search')}
            </button>
          </div>

          <select id="memory-owner-select" class="select text-xs" style="width: 8.5rem;" title="筛选归属者">
            <option value="">全部归属者</option>
          </select>

          <button class="btn btn-outline" id="memory-reflect-btn" title="触发后台反思提炼">
            <span id="memory-reflect-icon" class="inline-flex">${icon('brain')}</span>
            <span>反思</span>
          </button>

          <details class="dropdown" id="memory-more-dropdown">
            <summary class="btn btn-outline" title="更多操作">
              <span>更多</span>
              ${icon('chevron_down')}
            </summary>
            <div class="dropdown-menu">
              <label class="dropdown-item" style="cursor: pointer;">
                ${icon('upload')}
                <span>导入 JSON</span>
                <input type="file" id="memory-import-input" accept=".json" style="display: none;" />
              </label>
              <button type="button" class="dropdown-item" id="memory-export-btn">
                ${icon('download')}
                <span>导出 JSON</span>
              </button>
              <button type="button" class="dropdown-item" id="memory-refresh-btn">
                ${icon('refresh')}
                <span>刷新列表</span>
              </button>
            </div>
          </details>

          <button class="btn btn-primary" id="memory-add-btn">
            ${icon('plus')}
            <span>新建记忆</span>
          </button>
        </div>
      </header>

      <!-- 4 Compact KPI Stats Cards -->
      <section id="memory-stats-container" class="grid grid-cols-2 sm:grid-cols-4 gap-3">
        <article class="card p-3 flex flex-col justify-between">
          <p class="text-xs text-muted font-medium">总记忆数</p>
          <p class="text-xl font-bold tracking-tight text-foreground mt-1" id="stat-total">-</p>
        </article>
        <article class="card p-3 flex flex-col justify-between">
          <p class="text-xs text-muted font-medium">公开记忆</p>
          <p class="text-xl font-bold tracking-tight text-foreground mt-1" id="stat-public">-</p>
        </article>
        <article class="card p-3 flex flex-col justify-between">
          <p class="text-xs text-muted font-medium">私有记忆</p>
          <p class="text-xl font-bold tracking-tight text-foreground mt-1" id="stat-private">-</p>
        </article>
        <article class="card p-3 flex flex-col justify-between">
          <p class="text-xs text-muted font-medium">归属者数</p>
          <p class="text-xl font-bold tracking-tight text-foreground mt-1" id="stat-owners">-</p>
        </article>
      </section>

      <!-- Active Filter & Bulk Actions Bar -->
      <div class="flex items-center justify-between gap-3 flex-wrap min-h-7">
        <div id="memory-active-filter" class="flex items-center gap-2"></div>
        <div id="memory-bulk-actions" class="flex items-center gap-2" style="display: none;"></div>
      </div>

      <!-- Data Table Card -->
      <div class="card overflow-hidden">
        <div class="table-container">
          <table class="table">
            <thead>
              <tr>
                <th style="width: 2.5rem; text-align: center;">
                  <input type="checkbox" id="memory-select-all" class="checkbox" aria-label="全选" />
                </th>
                <th style="width: 4rem;">来源</th>
                <th style="width: 7.5rem;">归属者</th>
                <th>内容</th>
                <th style="width: 10rem;">标签</th>
                <th style="width: 5.5rem;">可见性</th>
                <th style="width: 6rem;">重要度</th>
                <th style="width: 8.5rem;">创建时间</th>
                <th style="width: 4.5rem; text-align: right;">操作</th>
              </tr>
            </thead>
            <tbody id="memory-table-body">
              <tr>
                <td colspan="9" class="text-center text-muted" style="padding: 2.5rem;">
                  <span class="spinner"></span>
                  <span style="margin-left: 0.5rem;">加载中...</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div id="memory-pagination-container"></div>
      </div>
    </div>
  `;

  const searchInput = container.querySelector<HTMLInputElement>('#memory-search-input')!;
  const searchBtn = container.querySelector<HTMLButtonElement>('#memory-search-btn')!;
  const ownerSelect = container.querySelector<HTMLSelectElement>('#memory-owner-select')!;
  const addBtn = container.querySelector<HTMLButtonElement>('#memory-add-btn')!;
  const reflectBtn = container.querySelector<HTMLButtonElement>('#memory-reflect-btn')!;
  const reflectIcon = container.querySelector<HTMLElement>('#memory-reflect-icon')!;
  const exportBtn = container.querySelector<HTMLButtonElement>('#memory-export-btn')!;
  const importInput = container.querySelector<HTMLInputElement>('#memory-import-input')!;
  const refreshBtn = container.querySelector<HTMLButtonElement>('#memory-refresh-btn')!;

  const statTotal = container.querySelector<HTMLElement>('#stat-total')!;
  const statPublic = container.querySelector<HTMLElement>('#stat-public')!;
  const statPrivate = container.querySelector<HTMLElement>('#stat-private')!;
  const statOwners = container.querySelector<HTMLElement>('#stat-owners')!;

  const activeFilterEl = container.querySelector<HTMLElement>('#memory-active-filter')!;
  const bulkActionsEl = container.querySelector<HTMLElement>('#memory-bulk-actions')!;
  const selectAllCheckbox = container.querySelector<HTMLInputElement>('#memory-select-all')!;
  const tbody = container.querySelector<HTMLElement>('#memory-table-body')!;
  const paginationContainer = container.querySelector<HTMLElement>('#memory-pagination-container')!;

  async function loadStats() {
    try {
      stats = await api.getMemoryStats();
      renderStats();
    } catch {
      // non-critical
    }
  }

  function renderStats() {
    if (!stats) return;

    statTotal.textContent = String(stats.total);
    statPublic.textContent = String(stats.publicCount);
    statPrivate.textContent = String(stats.privateCount);

    const owners = Object.entries(stats.byOwner || {});
    statOwners.textContent = String(owners.length);

    // Update Owner Select dropdown options
    const currentSelected = ownerSelect.value;
    ownerSelect.innerHTML = `
      <option value="">全部归属者 (${stats.total})</option>
      ${owners
        .map(
          ([owner, count]) => `
        <option value="${escapeHtml(owner)}" ${currentSelected === owner ? 'selected' : ''}>
          ${escapeHtml(owner)} (${count})
        </option>
      `,
        )
        .join('')}
    `;
  }

  async function loadMemories() {
    if (isUnmounted) return;
    loading = true;
    renderTable();

    try {
      if (searchQuery) {
        const res = await api.searchMemories(searchQuery, pageSize, tokenStack.currentToken);
        if (isUnmounted) return;
        memories = res.memories;
        tokenStack.setNextToken(res.pagination?.pageToken ?? '');
        total = Number(res.pagination?.total ?? res.memories.length);
      } else {
        const res = await api.listMemories(pageSize, tokenStack.currentToken, ownerFilter);
        if (isUnmounted) return;
        memories = res.memories;
        tokenStack.setNextToken(res.pagination?.pageToken ?? '');
        total = Number(res.pagination?.total ?? res.memories.length);
      }
    } catch (err) {
      if (isUnmounted) return;
      toast.error('加载记忆失败: ' + (err instanceof Error ? err.message : String(err)));
      memories = [];
    } finally {
      if (!isUnmounted) {
        loading = false;
        renderTable();
      }
    }
  }

  function getSourceIcon(source: string): string {
    switch (source) {
      case 'extract':
        return 'sparkles';
      case 'manual':
        return 'pencil';
      case 'reflect':
        return 'brain';
      default:
        return 'help';
    }
  }

  function getSourceLabel(source: string): string {
    switch (source) {
      case 'extract':
        return '自动提取';
      case 'manual':
        return '手动添加';
      case 'reflect':
        return '反思生成';
      default:
        return source;
    }
  }

  function renderTable() {
    // Render active filter chip
    if (ownerFilter) {
      activeFilterEl.innerHTML = `
        <span class="badge badge-outline text-xs px-2 py-0.5 flex items-center gap-1.5">
          ${icon('user', 'w-3 h-3')}
          <span>归属者: <strong>${escapeHtml(ownerFilter)}</strong></span>
          <button class="btn btn-ghost btn-icon-sm" style="width: 1rem; height: 1rem; padding: 0; margin-left: 0.25rem;" id="clear-owner-filter" aria-label="清除筛选">
            ${icon('close', 'w-3 h-3')}
          </button>
        </span>
      `;
      activeFilterEl.querySelector('#clear-owner-filter')?.addEventListener('click', () => {
        ownerFilter = '';
        ownerSelect.value = '';
        tokenStack.reset();
        void loadMemories();
      });
    } else {
      activeFilterEl.innerHTML = '';
    }

    // Render bulk actions
    if (selectedIds.size > 0) {
      bulkActionsEl.style.display = 'flex';
      bulkActionsEl.innerHTML = `
        <div class="px-2.5 py-1 bg-muted border border-border rounded-md flex items-center gap-2 text-xs">
          <span class="text-muted">已选择 <strong>${selectedIds.size}</strong> 项</span>
          <button class="btn btn-destructive btn-sm" style="height: 1.5rem; padding: 0 0.5rem;" id="delete-selected-btn">
            ${icon('trash', 'w-3 h-3')}
            <span>批量删除</span>
          </button>
        </div>
      `;
      bulkActionsEl.querySelector('#delete-selected-btn')?.addEventListener('click', () => {
        void deleteSelected();
      });
    } else {
      bulkActionsEl.style.display = 'none';
      bulkActionsEl.innerHTML = '';
    }

    // Update Select All Checkbox
    const allSelected = memories.length > 0 && memories.every((m) => selectedIds.has(m.id));
    const someSelected = memories.some((m) => selectedIds.has(m.id)) && !allSelected;
    selectAllCheckbox.checked = allSelected;
    selectAllCheckbox.indeterminate = someSelected;

    if (loading && memories.length === 0) {
      tbody.innerHTML = `
        <tr>
          <td colspan="9" class="text-center text-muted" style="padding: 2.5rem;">
            <span class="spinner"></span>
            <span style="margin-left: 0.5rem;">加载中...</span>
          </td>
        </tr>
      `;
      paginationContainer.innerHTML = '';
      return;
    }

    if (memories.length === 0) {
      tbody.innerHTML = `
        <tr>
          <td colspan="9" class="text-center text-muted" style="padding: 3rem;">
            暂无记忆记录。
          </td>
        </tr>
      `;
      paginationContainer.innerHTML = '';
      return;
    }

    tbody.innerHTML = memories
      .map((mem) => {
        const isChecked = selectedIds.has(mem.id);

        // Max 3 tags, rest summarized with +N badge
        const allTags = mem.tags || [];
        const displayedTags = allTags.slice(0, 3);
        const remainingCount = allTags.length - 3;

        const tagsHtml =
          allTags.length > 0
            ? `
            <div class="flex items-center gap-1 flex-wrap">
              ${displayedTags
                .map(
                  (t) => `
                <span class="badge badge-secondary text-[11px] px-1.5 py-0">${escapeHtml(t)}</span>
              `,
                )
                .join('')}
              ${
                remainingCount > 0
                  ? `<span class="badge badge-outline text-[11px] px-1 py-0 text-muted" title="${escapeHtml(
                      allTags.slice(3).join(', '),
                    )}">+${remainingCount}</span>`
                  : ''
              }
            </div>
          `
            : '<span class="text-muted text-xs">-</span>';

        const isPublic = mem.visibility === 'public';
        const visibilityBadge = isPublic
          ? `<span class="badge badge-success text-[11px] px-1.5 py-0 gap-1">${icon('globe', 'w-3 h-3')}<span>public</span></span>`
          : `<span class="badge badge-outline text-[11px] px-1.5 py-0 gap-1 text-muted">${icon('lock', 'w-3 h-3')}<span>private</span></span>`;

        const importancePct = Math.round((mem.importance || 0) * 100);

        return `
          <tr>
            <td style="text-align: center;">
              <input type="checkbox" class="checkbox row-checkbox" data-id="${escapeHtml(mem.id)}" ${isChecked ? 'checked' : ''} />
            </td>
            <td>
              <span title="${escapeHtml(getSourceLabel(mem.source))}" class="inline-flex items-center text-muted hover:text-foreground transition-colors cursor-help">
                ${icon(getSourceIcon(mem.source), 'w-3.5 h-3.5')}
              </span>
            </td>
            <td>
              <button class="badge badge-outline text-xs px-2 py-0.5 cursor-pointer flex items-center gap-1 hover:bg-muted transition-colors font-normal" data-action="filter-owner" data-owner="${escapeHtml(mem.owner)}" title="筛选此归属者">
                ${icon('user', 'w-3 h-3 text-muted')}
                <span>${escapeHtml(mem.owner)}</span>
              </button>
            </td>
            <td style="max-width: 22rem;">
              <button class="btn btn-ghost text-left text-xs block w-full p-0 font-normal hover:text-primary leading-snug" style="max-height: 2.5rem; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; text-overflow: ellipsis; white-space: normal;" data-action="edit-memory" data-id="${escapeHtml(mem.id)}" title="${escapeHtml(mem.content)}">
                ${escapeHtml(mem.content)}
              </button>
            </td>
            <td>${tagsHtml}</td>
            <td>${visibilityBadge}</td>
            <td>
              <div class="flex items-center gap-1.5">
                <div style="background-color: var(--muted); height: 0.25rem; width: 2.25rem; border-radius: var(--radius-full); overflow: hidden; flex-shrink: 0;">
                  <div style="background-color: var(--muted-foreground); height: 100%; width: ${importancePct}%;"></div>
                </div>
                <span class="text-xs text-muted font-mono">${importancePct}%</span>
              </div>
            </td>
            <td class="text-xs text-muted font-mono">${escapeHtml(formatDateTime(mem.createdAt))}</td>
            <td style="text-align: right;">
              <div class="flex items-center justify-end gap-1">
                <button class="btn btn-ghost btn-icon-sm" style="width: 1.75rem; height: 1.75rem;" data-action="edit-memory" data-id="${escapeHtml(mem.id)}" title="查看/编辑">
                  ${icon('eye', 'w-3.5 h-3.5')}
                </button>
                <button class="btn btn-ghost btn-icon-sm text-destructive" style="width: 1.75rem; height: 1.75rem;" data-action="delete-memory" data-id="${escapeHtml(mem.id)}" title="删除">
                  ${icon('trash', 'w-3.5 h-3.5')}
                </button>
              </div>
            </td>
          </tr>
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
        void loadMemories();
      },
      onPrevPage: () => {
        tokenStack.prev();
        void loadMemories();
      },
      onNextPage: () => {
        tokenStack.next();
        void loadMemories();
      },
    });

    // Attach row checkbox events
    tbody.querySelectorAll<HTMLInputElement>('.row-checkbox').forEach((cb) => {
      cb.addEventListener('change', () => {
        const id = cb.dataset.id;
        if (!id) return;
        if (cb.checked) {
          selectedIds.add(id);
        } else {
          selectedIds.delete(id);
        }
        renderTable();
      });
    });

    // Filter by clicking owner badge in row
    tbody.querySelectorAll<HTMLButtonElement>('[data-action="filter-owner"]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const owner = btn.dataset.owner;
        if (owner) {
          ownerFilter = owner;
          ownerSelect.value = owner;
          searchQuery = '';
          searchInput.value = '';
          tokenStack.reset();
          void loadMemories();
        }
      });
    });

    tbody.querySelectorAll<HTMLButtonElement>('[data-action="edit-memory"]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const id = btn.dataset.id;
        const mem = memories.find((m) => m.id === id);
        if (mem) openDetailDialog(mem);
      });
    });

    tbody.querySelectorAll<HTMLButtonElement>('[data-action="delete-memory"]').forEach((btn) => {
      btn.addEventListener('click', async () => {
        const id = btn.dataset.id;
        if (!id) return;
        const confirmed = await confirmDialog({
          title: '删除记忆',
          message: '确定要删除这条记忆吗？此操作不可逆。',
          confirmLabel: '删除',
          destructive: true,
        });
        if (confirmed) {
          try {
            const res = await api.deleteMemory(id);
            if (res.success) {
              toast.success('记忆已删除');
              selectedIds.delete(id);
              void loadStats();
              void loadMemories();
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

  async function deleteSelected() {
    const ids = [...selectedIds];
    if (ids.length === 0) return;

    const confirmed = await confirmDialog({
      title: '批量删除记忆',
      message: `确定要删除选中的 ${ids.length} 条记忆吗？此操作不可逆。`,
      confirmLabel: '批量删除',
      destructive: true,
    });
    if (!confirmed) return;

    let success = 0;
    let fail = 0;
    for (const id of ids) {
      try {
        const res = await api.deleteMemory(id);
        if (res.success) success++;
        else fail++;
      } catch {
        fail++;
      }
    }

    selectedIds.clear();
    const msg = `已删除 ${success} 条记忆${fail ? `，${fail} 条失败` : ''}`;
    if (fail > 0) toast.warning(msg);
    else toast.success(msg);

    void loadStats();
    void loadMemories();
  }

  function openAddDialog() {
    openDialog({
      title: '添加记忆',
      maxWidth: '32rem',
      bodyHtml: `
        <div class="flex flex-col gap-3.5">
          <div class="form-group">
            <label class="form-label" for="add-mem-owner">归属者</label>
            <input id="add-mem-owner" class="input text-xs" placeholder="webui" value="webui" />
          </div>
          <div class="form-group">
            <label class="form-label" for="add-mem-content">内容 <span class="text-destructive">*</span></label>
            <textarea id="add-mem-content" class="textarea text-xs" rows="4" placeholder="输入需要记录的记忆内容..."></textarea>
          </div>
          <div class="form-group">
            <label class="form-label" for="add-mem-tags">标签（逗号分隔）</label>
            <input id="add-mem-tags" class="input text-xs" placeholder="tag1, tag2" />
          </div>
          <div class="form-group">
            <label class="form-label" for="add-mem-visibility">可见性</label>
            <select id="add-mem-visibility" class="select text-xs">
              <option value="private">🔒 Private (私有)</option>
              <option value="public">🌐 Public (公开)</option>
            </select>
          </div>
        </div>
      `,
      footerHtml: `
        <button class="btn btn-outline btn-sm" id="add-mem-cancel">取消</button>
        <button class="btn btn-primary btn-sm" id="add-mem-save">保存</button>
      `,
      onMount: (dialogEl, close) => {
        const cancelBtn = dialogEl.querySelector('#add-mem-cancel')!;
        const saveBtn = dialogEl.querySelector<HTMLButtonElement>('#add-mem-save')!;
        const ownerInput = dialogEl.querySelector<HTMLInputElement>('#add-mem-owner')!;
        const contentInput = dialogEl.querySelector<HTMLTextAreaElement>('#add-mem-content')!;
        const tagsInput = dialogEl.querySelector<HTMLInputElement>('#add-mem-tags')!;
        const visSelect = dialogEl.querySelector<HTMLSelectElement>('#add-mem-visibility')!;

        cancelBtn.addEventListener('click', () => close());
        saveBtn.addEventListener('click', async () => {
          const content = contentInput.value.trim();
          if (!content) {
            toast.error('记忆内容不能为空');
            return;
          }
          const owner = ownerInput.value.trim() || 'webui';
          const tags = tagsInput.value
            .split(',')
            .map((t) => t.trim())
            .filter(Boolean);
          const visibility = visSelect.value;

          try {
            saveBtn.disabled = true;
            const res = await api.addMemory(owner, content, tags, visibility);
            if (res.memory) {
              toast.success('记忆已添加');
              close();
              void loadStats();
              void loadMemories();
            } else {
              toast.error('添加失败: ' + res.error);
            }
          } catch (err) {
            toast.error('添加失败: ' + (err instanceof Error ? err.message : String(err)));
          } finally {
            saveBtn.disabled = false;
          }
        });
      },
    });
  }

  function openDetailDialog(mem: MemoryEntry) {
    let currentImportance = mem.importance || 0;

    openDialog({
      title: '记忆详情与编辑',
      maxWidth: '38rem',
      bodyHtml: `
        <div class="flex flex-col gap-3.5">
          <div class="grid grid-cols-2 gap-2 p-3 rounded-md border border-border bg-muted text-xs">
            <div class="col-span-2">
              <span class="text-muted">ID:</span>
              <span class="font-mono ml-1 break-all text-foreground select-all">${escapeHtml(mem.id)}</span>
            </div>
            <div>
              <span class="text-muted">归属者:</span>
              <span class="ml-1 font-medium text-foreground">${escapeHtml(mem.owner)}</span>
            </div>
            <div>
              <span class="text-muted">来源:</span>
              <span class="ml-1 text-foreground">${escapeHtml(getSourceLabel(mem.source))}</span>
            </div>
            <div>
              <span class="text-muted">创建时间:</span>
              <span class="ml-1 font-mono text-foreground">${escapeHtml(formatDateTime(mem.createdAt))}</span>
            </div>
            <div>
              <span class="text-muted">更新时间:</span>
              <span class="ml-1 font-mono text-foreground">${escapeHtml(formatDateTime(mem.updatedAt))}</span>
            </div>
          </div>

          <div class="form-group">
            <label class="form-label" for="edit-mem-content">内容</label>
            <textarea id="edit-mem-content" class="textarea text-xs" rows="4">${escapeHtml(mem.content)}</textarea>
          </div>

          <div class="form-group">
            <label class="form-label" for="edit-mem-tags">标签（逗号分隔）</label>
            <input id="edit-mem-tags" class="input text-xs" value="${escapeHtml((mem.tags || []).join(', '))}" />
          </div>

          <div class="form-group">
            <label class="form-label" for="edit-mem-visibility">可见性</label>
            <select id="edit-mem-visibility" class="select text-xs">
              <option value="private" ${mem.visibility === 'private' ? 'selected' : ''}>🔒 Private (私有)</option>
              <option value="public" ${mem.visibility === 'public' ? 'selected' : ''}>🌐 Public (公开)</option>
            </select>
          </div>

          <div class="form-group">
            <div class="flex items-center justify-between">
              <label class="form-label" for="edit-mem-importance">重要度</label>
              <span id="importance-val" class="text-xs font-mono font-medium text-foreground">${(currentImportance * 100).toFixed(0)}%</span>
            </div>
            <input type="range" id="edit-mem-importance" min="0" max="1" step="0.01" value="${currentImportance}" class="w-full" />
          </div>
        </div>
      `,
      footerHtml: `
        <button class="btn btn-outline btn-sm" id="edit-mem-cancel">取消</button>
        <button class="btn btn-primary btn-sm" id="edit-mem-save">保存更新</button>
      `,
      onMount: (dialogEl, close) => {
        const cancelBtn = dialogEl.querySelector('#edit-mem-cancel')!;
        const saveBtn = dialogEl.querySelector<HTMLButtonElement>('#edit-mem-save')!;
        const contentInput = dialogEl.querySelector<HTMLTextAreaElement>('#edit-mem-content')!;
        const tagsInput = dialogEl.querySelector<HTMLInputElement>('#edit-mem-tags')!;
        const visSelect = dialogEl.querySelector<HTMLSelectElement>('#edit-mem-visibility')!;
        const impRange = dialogEl.querySelector<HTMLInputElement>('#edit-mem-importance')!;
        const impVal = dialogEl.querySelector<HTMLElement>('#importance-val')!;

        impRange.addEventListener('input', () => {
          const val = parseFloat(impRange.value);
          currentImportance = val;
          impVal.textContent = `${(val * 100).toFixed(0)}%`;
        });

        cancelBtn.addEventListener('click', () => close());
        saveBtn.addEventListener('click', async () => {
          const content = contentInput.value.trim();
          if (!content) {
            toast.error('记忆内容不能为空');
            return;
          }
          const tags = tagsInput.value
            .split(',')
            .map((t) => t.trim())
            .filter(Boolean);
          const visibility = visSelect.value;

          try {
            saveBtn.disabled = true;
            const res = await api.updateMemory(mem.id, content, tags, visibility, currentImportance);
            if (res.success) {
              toast.success('记忆已更新');
              close();
              void loadMemories();
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

  // Trigger reflection
  async function triggerReflection() {
    if (reflecting) return;
    reflecting = true;
    reflectBtn.disabled = true;
    reflectIcon.innerHTML = `<span class="spinner inline-block" style="width: 0.875rem; height: 0.875rem;"></span>`;

    try {
      const result = await api.triggerMemoryReflection(ownerFilter);
      if (result.error) {
        toast.error('启动反思失败: ' + result.error);
        return;
      }
      if (!result.started) {
        toast.warning('已有记忆反思任务正在后台运行');
        return;
      }
      const scope = ownerFilter ? `用户 ${ownerFilter}` : '全部用户';
      toast.success(`已在后台启动 ${scope} 的记忆反思`);
    } catch (err) {
      toast.error('启动反思失败: ' + (err instanceof Error ? err.message : String(err)));
    } finally {
      reflecting = false;
      reflectBtn.disabled = false;
      reflectIcon.innerHTML = icon('brain');
    }
  }

  // Export JSON
  async function exportMemories() {
    try {
      const res = await api.exportMemories();
      if (res.error) {
        toast.error('导出失败: ' + res.error);
        return;
      }
      const blob = new Blob([res.jsonContent], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `memories-export-${new Date().toISOString().slice(0, 10)}.json`;
      a.click();
      URL.revokeObjectURL(url);
      toast.success('导出成功');
    } catch (err) {
      toast.error('导出失败: ' + (err instanceof Error ? err.message : String(err)));
    }
  }

  // Import JSON
  function handleImport(e: Event) {
    const input = e.target as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;

    const reader = new FileReader();
    reader.onload = async () => {
      try {
        const res = await api.importMemories(reader.result as string, false);
        if (res.error) {
          toast.error('导入失败: ' + res.error);
        } else {
          toast.success(`已导入 ${res.imported} 条，跳过 ${res.skipped} 条`);
          void loadStats();
          void loadMemories();
        }
      } catch (err) {
        toast.error('导入失败: ' + (err instanceof Error ? err.message : String(err)));
      }
    };
    reader.readAsText(file);
    input.value = '';
  }

  // Event Listeners
  searchBtn.addEventListener('click', () => {
    searchQuery = searchInput.value.trim();
    ownerFilter = '';
    ownerSelect.value = '';
    tokenStack.reset();
    void loadMemories();
  });

  searchInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      searchQuery = searchInput.value.trim();
      ownerFilter = '';
      ownerSelect.value = '';
      tokenStack.reset();
      void loadMemories();
    }
  });

  ownerSelect.addEventListener('change', () => {
    ownerFilter = ownerSelect.value;
    searchQuery = '';
    searchInput.value = '';
    tokenStack.reset();
    void loadMemories();
  });

  selectAllCheckbox.addEventListener('change', () => {
    if (selectAllCheckbox.checked) {
      memories.forEach((m) => selectedIds.add(m.id));
    } else {
      selectedIds.clear();
    }
    renderTable();
  });

  addBtn.addEventListener('click', openAddDialog);
  reflectBtn.addEventListener('click', triggerReflection);
  exportBtn.addEventListener('click', exportMemories);
  importInput.addEventListener('change', handleImport);
  refreshBtn.addEventListener('click', () => {
    tokenStack.reset();
    ownerFilter = '';
    ownerSelect.value = '';
    searchQuery = '';
    searchInput.value = '';
    selectedIds.clear();
    void loadStats();
    void loadMemories();
  });

  // Initial load
  void loadStats();
  void loadMemories();

  return () => {
    isUnmounted = true;
  };
}
