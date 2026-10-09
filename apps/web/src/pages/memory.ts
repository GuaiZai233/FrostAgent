import { createInstanceAPI } from '../api/client';
import {
  MemoryEntry,
  GetMemoryStatsResponse,
  GroupSummary,
  GroupProfile,
  MemberProfile,
} from '@frostagent/proto';
import { escapeHtml, formatCount, formatDateTime, PageTokenStack } from '../utils/formatters';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { openDialog } from '../components/dialog';
import { confirmDialog } from '../components/confirm';
import { renderPagination, attachPaginationEvents } from '../components/pagination';

export function mountMemoryPage(container: HTMLElement): () => void {
  const api = createInstanceAPI();
  let isUnmounted = false;
  let loading = false;
  let reflecting = false;

  // Scope state: 'private' | 'group'
  let currentScope: 'private' | 'group' = 'private';
  let groupSubTab: 'memories' | 'members' = 'memories';

  // Group state
  let groups: GroupSummary[] = [];
  let selectedGroupId = '';
  let selectedGroupProfile: GroupProfile | null = null;
  let loadingProfile = false;

  // Memories & stats state
  let memories: MemoryEntry[] = [];
  let stats: GetMemoryStatsResponse | null = null;
  let total = 0;
  let pageSize = 20;
  let searchQuery = '';
  let ownerFilter = '';
  const selectedIds = new Set<string>();
  const tokenStack = new PageTokenStack();

  function renderSkeleton() {
    container.innerHTML = `
      <div class="page-container fade-in">
        <header class="flex items-center justify-between gap-4 flex-wrap pb-1">
          <div>
            <h1 class="page-title">记忆管理</h1>
            <p class="page-description">管理 Bot 隔离化长期记忆、群聊档案与成员称呼</p>
          </div>
          <div class="flex items-center gap-2 flex-wrap" id="memory-header-actions">
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

            <select id="memory-owner-select" class="select text-xs" style="width: 9rem;" title="筛选归属者">
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

        <!-- 4 Compact KPI Stats Cards (Scoped isolation stats) -->
        <section id="memory-stats-container" class="grid grid-cols-2 sm:grid-cols-4 gap-3">
          <article class="card p-3 flex flex-col gap-1">
            <p class="text-xs text-muted font-medium">总记忆数</p>
            <p class="text-xl font-bold tracking-tight text-foreground" id="stat-total">-</p>
          </article>
          <article class="card p-3 flex flex-col gap-1">
            <p class="text-xs text-muted font-medium">私聊记忆</p>
            <p class="text-xl font-bold tracking-tight text-foreground" id="stat-private">-</p>
          </article>
          <article class="card p-3 flex flex-col gap-1">
            <p class="text-xs text-muted font-medium">群聊记忆</p>
            <p class="text-xl font-bold tracking-tight text-foreground" id="stat-group">-</p>
          </article>
          <article class="card p-3 flex flex-col gap-1">
            <p class="text-xs text-muted font-medium">涉及群组数</p>
            <p class="text-xl font-bold tracking-tight text-foreground" id="stat-groups-count">-</p>
          </article>
        </section>

        <!-- Scope Tabs: 私聊记忆 vs 群聊记忆 -->
        <div class="flex items-center justify-between gap-3 flex-wrap">
          <div class="tabs" role="tablist">
            <button class="tab-item ${currentScope === 'private' ? 'active' : ''}" data-scope-tab="private">
              ${icon('user', 'w-3.5 h-3.5')}
              <span>私聊记忆</span>
            </button>
            <button class="tab-item ${currentScope === 'group' ? 'active' : ''}" data-scope-tab="group">
              ${icon('users', 'w-3.5 h-3.5')}
              <span>群聊记忆</span>
            </button>
          </div>

          <!-- Group Scope: Group Selector -->
          <div id="group-selector-container" class="flex items-center gap-2" style="${currentScope === 'group' ? '' : 'display: none;'}">
            <label class="text-xs text-muted font-medium" for="group-select">选择群组:</label>
            <select id="group-select" class="select text-xs" style="min-width: 12rem;">
              <option value="">加载群聊中...</option>
            </select>
            <button class="btn btn-outline btn-icon-sm" id="group-refresh-btn" title="刷新群聊列表">
              ${icon('refresh', 'w-3.5 h-3.5')}
            </button>
          </div>
        </div>

        <!-- Dynamic Body: Private vs Group -->
        <div id="memory-scope-content" class="flex flex-col gap-3"></div>
      </div>
    `;

    attachHeaderEvents();
  }

  function attachHeaderEvents() {
    const scopeButtons = container.querySelectorAll<HTMLButtonElement>('[data-scope-tab]');
    scopeButtons.forEach((btn) => {
      btn.addEventListener('click', () => {
        const target = btn.dataset.scopeTab as 'private' | 'group';
        if (target && target !== currentScope) {
          currentScope = target;
          tokenStack.reset();
          selectedIds.clear();
          searchQuery = '';
          ownerFilter = '';
          renderSkeleton();
          renderStats();
          if (currentScope === 'group') {
            void loadGroupsAndData();
          } else {
            void loadMemories();
          }
        }
      });
    });

    const searchInput = container.querySelector<HTMLInputElement>('#memory-search-input')!;
    const searchBtn = container.querySelector<HTMLButtonElement>('#memory-search-btn')!;
    const ownerSelect = container.querySelector<HTMLSelectElement>('#memory-owner-select')!;
    const reflectBtn = container.querySelector<HTMLButtonElement>('#memory-reflect-btn')!;
    const exportBtn = container.querySelector<HTMLButtonElement>('#memory-export-btn')!;
    const importInput = container.querySelector<HTMLInputElement>('#memory-import-input')!;
    const refreshBtn = container.querySelector<HTMLButtonElement>('#memory-refresh-btn')!;
    const addBtn = container.querySelector<HTMLButtonElement>('#memory-add-btn')!;

    searchBtn?.addEventListener('click', () => {
      searchQuery = searchInput.value.trim();
      ownerFilter = '';
      if (ownerSelect) ownerSelect.value = '';
      tokenStack.reset();
      void loadMemories();
    });

    searchInput?.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        searchQuery = searchInput.value.trim();
        ownerFilter = '';
        if (ownerSelect) ownerSelect.value = '';
        tokenStack.reset();
        void loadMemories();
      }
    });

    ownerSelect?.addEventListener('change', () => {
      ownerFilter = ownerSelect.value;
      searchQuery = '';
      if (searchInput) searchInput.value = '';
      tokenStack.reset();
      void loadMemories();
    });

    reflectBtn?.addEventListener('click', () => void triggerReflection());
    exportBtn?.addEventListener('click', () => void exportMemories());
    importInput?.addEventListener('change', handleImport);
    refreshBtn?.addEventListener('click', () => {
      tokenStack.reset();
      ownerFilter = '';
      if (ownerSelect) ownerSelect.value = '';
      searchQuery = '';
      if (searchInput) searchInput.value = '';
      selectedIds.clear();
      void loadStats();
      if (currentScope === 'group') {
        void loadGroupsAndData();
      } else {
        void loadMemories();
      }
    });

    addBtn?.addEventListener('click', openAddDialog);

    const groupRefreshBtn = container.querySelector<HTMLButtonElement>('#group-refresh-btn');
    groupRefreshBtn?.addEventListener('click', () => {
      void loadGroupsAndData();
    });

    const groupSelect = container.querySelector<HTMLSelectElement>('#group-select');
    groupSelect?.addEventListener('change', () => {
      selectedGroupId = groupSelect.value;
      tokenStack.reset();
      selectedIds.clear();
      void loadGroupProfileAndMemories();
    });
  }

  async function loadStats() {
    try {
      stats = await api.getMemoryStats(
        currentScope,
        currentScope === 'group' ? selectedGroupId : '',
      );
      renderStats();
    } catch {
      // non-critical
    }
  }

  function renderStats() {
    if (!stats) return;

    const statTotal = container.querySelector<HTMLElement>('#stat-total');
    const statPrivate = container.querySelector<HTMLElement>('#stat-private');
    const statGroup = container.querySelector<HTMLElement>('#stat-group');
    const statGroupsCount = container.querySelector<HTMLElement>('#stat-groups-count');

    if (statTotal) statTotal.textContent = String(stats.total);
    if (statPrivate) statPrivate.textContent = String(stats.privateChatCount);
    if (statGroup) statGroup.textContent = String(stats.groupChatCount);
    if (statGroupsCount) statGroupsCount.textContent = String(stats.groupCount);

    // Update Owner Select dropdown options
    const ownerSelect = container.querySelector<HTMLSelectElement>('#memory-owner-select');
    if (ownerSelect) {
      const owners = Object.entries(stats.byOwner || {});
      const currentSelected = ownerSelect.value;
      ownerSelect.innerHTML = `
        <option value="">全部归属者</option>
        ${owners
          .map(
            ([owner, count]) => `
          <option value="${escapeHtml(owner)}" ${currentSelected === owner ? 'selected' : ''}>
            ${escapeHtml(owner === 'group' ? '群整体 (group)' : owner)} (${count})
          </option>
        `,
          )
          .join('')}
      `;
    }
  }

  async function loadGroupsAndData() {
    if (isUnmounted) return;
    try {
      const res = await api.listGroups();
      if (isUnmounted) return;
      groups = res.groups || [];
      if (!selectedGroupId && groups.length > 0) {
        selectedGroupId = groups[0].groupId;
      } else if (selectedGroupId && !groups.some((g) => g.groupId === selectedGroupId)) {
        selectedGroupId = groups.length > 0 ? groups[0].groupId : '';
      }
      renderGroupSelector();
      if (selectedGroupId) {
        await loadGroupProfileAndMemories();
      } else {
        renderScopeContent();
      }
    } catch (err) {
      if (isUnmounted) return;
      toast.error('加载群聊列表失败: ' + (err instanceof Error ? err.message : String(err)));
      renderScopeContent();
    }
  }

  function renderGroupSelector() {
    const groupSelect = container.querySelector<HTMLSelectElement>('#group-select');
    if (!groupSelect) return;

    if (groups.length === 0) {
      groupSelect.innerHTML = `<option value="">未观测到任何群聊</option>`;
      groupSelect.disabled = true;
      return;
    }

    groupSelect.disabled = false;
    groupSelect.innerHTML = groups
      .map(
        (g) => `
        <option value="${escapeHtml(g.groupId)}" ${g.groupId === selectedGroupId ? 'selected' : ''}>
          ${escapeHtml(g.groupName ? `${g.groupName} (${g.groupId})` : `群 ${g.groupId}`)} · ${g.memberCount} 人 · ${g.memoryCount} 条
        </option>
      `,
      )
      .join('');
  }

  async function loadGroupProfileAndMemories() {
    if (!selectedGroupId || isUnmounted) return;
    loadingProfile = true;
    try {
      const profRes = await api.getGroupProfile(selectedGroupId);
      if (isUnmounted) return;
      selectedGroupProfile = profRes.profile ?? null;
    } catch {
      selectedGroupProfile = null;
    } finally {
      loadingProfile = false;
    }

    await loadMemories();
  }

  async function loadMemories() {
    if (isUnmounted) return;
    loading = true;
    renderScopeContent();

    try {
      const scopeParam = currentScope;
      const groupParam = currentScope === 'group' ? selectedGroupId : '';

      if (currentScope === 'group' && !selectedGroupId) {
        memories = [];
        total = 0;
        return;
      }

      if (searchQuery) {
        const res = await api.searchMemories(
          searchQuery,
          pageSize,
          tokenStack.currentToken,
          scopeParam,
          groupParam,
        );
        if (isUnmounted) return;
        memories = res.memories;
        tokenStack.setNextToken(res.pagination?.pageToken ?? '');
        total = Number(res.pagination?.total ?? res.memories.length);
      } else {
        const res = await api.listMemories(
          pageSize,
          tokenStack.currentToken,
          ownerFilter,
          scopeParam,
          groupParam,
        );
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
        renderScopeContent();
        void loadStats();
      }
    }
  }

  function renderScopeContent() {
    const scopeContentEl = container.querySelector<HTMLElement>('#memory-scope-content');
    if (!scopeContentEl) return;

    if (currentScope === 'private') {
      renderPrivateContent(scopeContentEl);
    } else {
      renderGroupContent(scopeContentEl);
    }
  }

  function renderPrivateContent(el: HTMLElement) {
    el.innerHTML = `
      <!-- Active Filter & Bulk Actions Bar -->
      <div class="flex items-center justify-between gap-3 flex-wrap min-h-7">
        <div id="memory-active-filter" class="flex items-center gap-2"></div>
        <div id="memory-bulk-actions" class="flex items-center gap-2" style="display: none;"></div>
      </div>

      <!-- Data Table Card -->
      <div class="card table-card overflow-hidden">
        <div class="table-container">
          <table class="table">
            <thead>
              <tr>
                <th style="width: 2.5rem; text-align: center;">
                  <input type="checkbox" id="memory-select-all" class="checkbox" aria-label="全选" />
                </th>
                <th style="width: 4rem;">来源</th>
                <th style="width: 8rem;">归属者 (QQ)</th>
                <th>记忆内容</th>
                <th style="width: 10rem;">标签</th>
                <th style="width: 5.5rem;">召回次数</th>
                <th style="width: 8.5rem;">创建时间</th>
                <th style="width: 4.5rem; text-align: right;">操作</th>
              </tr>
            </thead>
            <tbody id="memory-table-body">
              ${renderTableRowsHtml()}
            </tbody>
          </table>
        </div>
        <div id="memory-pagination-container"></div>
      </div>
    `;

    attachTableEvents(el);
  }

  function renderGroupContent(el: HTMLElement) {
    if (!selectedGroupId || groups.length === 0) {
      el.innerHTML = `
        <div class="card p-8 text-center text-muted">
          ${icon('users', 'w-10 h-10 mx-auto mb-2 text-muted')}
          <p class="font-medium text-foreground">暂无群聊数据</p>
          <p class="text-xs text-muted mt-1">当 Bot 在群聊中收到或发送消息后，将自动观测并收录群聊信息与独立群记忆。</p>
        </div>
      `;
      return;
    }

    const currentGroupSummary = groups.find((g) => g.groupId === selectedGroupId);
    const groupName =
      selectedGroupProfile?.groupName || currentGroupSummary?.groupName || '未命名群聊';
    const memberCount =
      selectedGroupProfile?.members?.length ?? currentGroupSummary?.memberCount ?? 0;
    const memoryCount = currentGroupSummary?.memoryCount ?? memories.length;

    el.innerHTML = `
      <!-- Group Profile Header Card -->
      <div class="card p-4 flex items-center justify-between gap-4 flex-wrap">
        <div class="flex items-center gap-3">
          <div class="w-10 h-10 rounded-full bg-primary/10 text-primary flex items-center justify-center font-bold">
            ${icon('users', 'w-5 h-5')}
          </div>
          <div>
            <div class="flex items-center gap-2">
              <h2 class="text-base font-semibold text-foreground tracking-tight">${escapeHtml(groupName)}</h2>
              <button class="btn btn-ghost btn-icon-sm" id="edit-group-name-btn" title="编辑群名称">
                ${icon('pencil', 'w-3.5 h-3.5')}
              </button>
            </div>
            <div class="flex items-center gap-3 text-xs text-muted mt-0.5 font-mono">
              <span>群号: <strong>${escapeHtml(selectedGroupId)}</strong></span>
              <span>·</span>
              <span>成员: <strong>${memberCount}</strong> 人</span>
              <span>·</span>
              <span>群记忆: <strong>${memoryCount}</strong> 条</span>
            </div>
          </div>
        </div>

        <!-- Subtabs: 群记忆 vs 群成员 -->
        <div class="tabs" role="tablist">
          <button class="tab-item ${groupSubTab === 'memories' ? 'active' : ''}" data-group-subtab="memories">
            ${icon('brain', 'w-3.5 h-3.5')}
            <span>群记忆条目</span>
          </button>
          <button class="tab-item ${groupSubTab === 'members' ? 'active' : ''}" data-group-subtab="members">
            ${icon('users', 'w-3.5 h-3.5')}
            <span>群成员与称呼 (${memberCount})</span>
          </button>
        </div>
      </div>

      <!-- Subtab Content -->
      <div id="group-subtab-content">
        ${groupSubTab === 'memories' ? renderGroupMemoriesHtml() : renderGroupMembersHtml()}
      </div>
    `;

    // Attach subtab switcher events
    el.querySelectorAll<HTMLButtonElement>('[data-group-subtab]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const sub = btn.dataset.groupSubtab as 'memories' | 'members';
        if (sub && sub !== groupSubTab) {
          groupSubTab = sub;
          renderGroupContent(el);
        }
      });
    });

    // Edit group name button
    el.querySelector('#edit-group-name-btn')?.addEventListener('click', () => {
      openEditGroupNameDialog(groupName);
    });

    if (groupSubTab === 'memories') {
      attachTableEvents(el);
    } else {
      attachMemberEvents(el);
    }
  }

  function renderGroupMemoriesHtml(): string {
    return `
      <!-- Active Filter & Bulk Actions Bar -->
      <div class="flex items-center justify-between gap-3 flex-wrap min-h-7 mb-2">
        <div id="memory-active-filter" class="flex items-center gap-2"></div>
        <div id="memory-bulk-actions" class="flex items-center gap-2" style="display: none;"></div>
      </div>

      <!-- Data Table Card -->
      <div class="card table-card overflow-hidden">
        <div class="table-container">
          <table class="table">
            <thead>
              <tr>
                <th style="width: 2.5rem; text-align: center;">
                  <input type="checkbox" id="memory-select-all" class="checkbox" aria-label="全选" />
                </th>
                <th style="width: 4rem;">来源</th>
                <th style="width: 8rem;">归属 (群/成员)</th>
                <th>记忆内容</th>
                <th style="width: 10rem;">标签</th>
                <th style="width: 5.5rem;">召回次数</th>
                <th style="width: 8.5rem;">创建时间</th>
                <th style="width: 4.5rem; text-align: right;">操作</th>
              </tr>
            </thead>
            <tbody id="memory-table-body">
              ${renderTableRowsHtml()}
            </tbody>
          </table>
        </div>
        <div id="memory-pagination-container"></div>
      </div>
    `;
  }

  function renderGroupMembersHtml(): string {
    const members = selectedGroupProfile?.members || [];
    if (loadingProfile) {
      return `
        <div class="card p-8 text-center text-muted">
          <span class="spinner inline-block"></span>
          <span class="ml-2">正在加载群成员与称呼列表...</span>
        </div>
      `;
    }

    if (members.length === 0) {
      return `
        <div class="card p-8 text-center text-muted">
          暂无已记录的群成员。当群成员在群内发言时将自动记录。
        </div>
      `;
    }

    return `
      <div class="card table-card overflow-hidden">
        <div class="table-container">
          <table class="table">
            <thead>
              <tr>
                <th style="width: 8.5rem;">群员 QQ</th>
                <th style="width: 9rem;">群内昵称</th>
                <th style="width: 9rem;">群名片</th>
                <th style="width: 6rem;">群身份</th>
                <th style="width: 8rem;">生效称呼</th>
                <th style="width: 8rem;">优先称呼</th>
                <th>别名</th>
                <th style="width: 8.5rem;">最后发言</th>
                <th style="width: 5rem; text-align: right;">操作</th>
              </tr>
            </thead>
            <tbody>
              ${members
                .map((m) => {
                  const roleBadge = formatRoleBadge(m.role);
                  const aliasesHtml =
                    m.aliases && m.aliases.length > 0
                      ? `<div class="flex items-center gap-1 flex-wrap">${m.aliases
                          .map(
                            (a) =>
                              `<span class="badge badge-secondary text-[11px] px-1.5 py-0">${escapeHtml(a)}</span>`,
                          )
                          .join('')}</div>`
                      : '<span class="text-muted text-xs">-</span>';

                  return `
                    <tr>
                      <td class="font-mono text-xs font-medium text-foreground">${escapeHtml(m.userId)}</td>
                      <td class="text-xs">${escapeHtml(m.nickname || '-')}</td>
                      <td class="text-xs text-muted" title="群名片仅用于身份识别 disambiguation，不作为称呼">${escapeHtml(m.card || '-')}</td>
                      <td>${roleBadge}</td>
                      <td>
                        <span class="badge badge-primary text-xs font-semibold px-2 py-0.5">
                          ${escapeHtml(m.callingName || '群友')}
                        </span>
                      </td>
                      <td class="text-xs font-medium text-foreground">${escapeHtml(m.preferredName || '-')}</td>
                      <td>${aliasesHtml}</td>
                      <td class="text-xs text-muted font-mono">${escapeHtml(m.lastSpokeAt ? formatDateTime(m.lastSpokeAt) : '-')}</td>
                      <td style="text-align: right;">
                        <button class="btn btn-ghost btn-sm text-xs" data-action="edit-member" data-userid="${escapeHtml(m.userId)}" title="编辑优先称呼与别名">
                          ${icon('pencil', 'w-3 h-3')}
                          <span>编辑称呼</span>
                        </button>
                      </td>
                    </tr>
                  `;
                })
                .join('')}
            </tbody>
          </table>
        </div>
      </div>
    `;
  }

  function formatRoleBadge(role: string): string {
    switch (role) {
      case 'owner':
        return `<span class="badge badge-destructive text-[11px] px-1.5 py-0">群主</span>`;
      case 'admin':
        return `<span class="badge badge-warning text-[11px] px-1.5 py-0">管理员</span>`;
      case 'member':
        return `<span class="badge badge-outline text-[11px] px-1.5 py-0 text-muted">群员</span>`;
      default:
        return `<span class="badge badge-outline text-[11px] px-1.5 py-0 text-muted">未知</span>`;
    }
  }

  function renderTableRowsHtml(): string {
    if (loading && memories.length === 0) {
      return `
        <tr>
          <td colspan="8" class="text-center text-muted" style="padding: 2.5rem;">
            <span class="spinner inline-block"></span>
            <span style="margin-left: 0.5rem;">加载记忆中...</span>
          </td>
        </tr>
      `;
    }

    if (memories.length === 0) {
      return `
        <tr>
          <td colspan="8" class="text-center text-muted" style="padding: 3rem;">
            暂无记忆记录。
          </td>
        </tr>
      `;
    }

    return memories
      .map((mem) => {
        const isChecked = selectedIds.has(mem.id);
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

        const isGroupOwner = mem.owner === 'group';
        const ownerDisplay = isGroupOwner ? '群整体 (group)' : mem.owner;

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
                ${icon(isGroupOwner ? 'users' : 'user', 'w-3 h-3 text-muted')}
                <span>${escapeHtml(ownerDisplay)}</span>
              </button>
            </td>
            <td class="memory-content-cell">
              <button type="button" class="memory-content-preview" data-action="edit-memory" data-id="${escapeHtml(mem.id)}" title="${escapeHtml(mem.content)}">
                ${escapeHtml(mem.content)}
              </button>
            </td>
            <td>${tagsHtml}</td>
            <td>
              <span class="text-xs text-muted font-mono font-medium">${escapeHtml(formatCount(mem.accessCount))}</span>
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

  function attachTableEvents(rootEl: HTMLElement) {
    const activeFilterEl = rootEl.querySelector<HTMLElement>('#memory-active-filter');
    const bulkActionsEl = rootEl.querySelector<HTMLElement>('#memory-bulk-actions');
    const selectAllCheckbox = rootEl.querySelector<HTMLInputElement>('#memory-select-all');
    const paginationContainer = rootEl.querySelector<HTMLElement>('#memory-pagination-container');

    // Render active filter chip
    if (activeFilterEl) {
      if (ownerFilter) {
        activeFilterEl.innerHTML = `
          <span class="badge badge-outline text-xs px-2 py-0.5 flex items-center gap-1.5">
            ${icon('user', 'w-3 h-3')}
            <span>归属: <strong>${escapeHtml(ownerFilter === 'group' ? '群整体 (group)' : ownerFilter)}</strong></span>
            <button class="btn btn-ghost btn-icon-sm" style="width: 1rem; height: 1rem; padding: 0; margin-left: 0.25rem;" id="clear-owner-filter" aria-label="清除筛选">
              ${icon('close', 'w-3 h-3')}
            </button>
          </span>
        `;
        activeFilterEl.querySelector('#clear-owner-filter')?.addEventListener('click', () => {
          ownerFilter = '';
          const ownerSelect = container.querySelector<HTMLSelectElement>('#memory-owner-select');
          if (ownerSelect) ownerSelect.value = '';
          tokenStack.reset();
          void loadMemories();
        });
      } else {
        activeFilterEl.innerHTML = '';
      }
    }

    // Render bulk actions
    if (bulkActionsEl) {
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
    }

    // Update Select All Checkbox
    if (selectAllCheckbox) {
      const allSelected = memories.length > 0 && memories.every((m) => selectedIds.has(m.id));
      const someSelected = memories.some((m) => selectedIds.has(m.id)) && !allSelected;
      selectAllCheckbox.checked = allSelected;
      selectAllCheckbox.indeterminate = someSelected;

      selectAllCheckbox.addEventListener('change', () => {
        if (selectAllCheckbox.checked) {
          memories.forEach((m) => selectedIds.add(m.id));
        } else {
          selectedIds.clear();
        }
        renderScopeContent();
      });
    }

    // Pagination
    if (paginationContainer) {
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
    }

    // Row Checkboxes
    rootEl.querySelectorAll<HTMLInputElement>('.row-checkbox').forEach((cb) => {
      cb.addEventListener('change', () => {
        const id = cb.dataset.id;
        if (!id) return;
        if (cb.checked) {
          selectedIds.add(id);
        } else {
          selectedIds.delete(id);
        }
        renderScopeContent();
      });
    });

    // Owner filter click
    rootEl.querySelectorAll<HTMLButtonElement>('[data-action="filter-owner"]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const owner = btn.dataset.owner;
        if (owner) {
          ownerFilter = owner;
          const ownerSelect = container.querySelector<HTMLSelectElement>('#memory-owner-select');
          if (ownerSelect) ownerSelect.value = owner;
          searchQuery = '';
          const searchInput = container.querySelector<HTMLInputElement>('#memory-search-input');
          if (searchInput) searchInput.value = '';
          tokenStack.reset();
          void loadMemories();
        }
      });
    });

    // Edit memory click
    rootEl.querySelectorAll<HTMLButtonElement>('[data-action="edit-memory"]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const id = btn.dataset.id;
        const mem = memories.find((m) => m.id === id);
        if (mem) openDetailDialog(mem);
      });
    });

    // Delete memory click
    rootEl.querySelectorAll<HTMLButtonElement>('[data-action="delete-memory"]').forEach((btn) => {
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
            const scopeParam = currentScope;
            const groupParam = currentScope === 'group' ? selectedGroupId : '';
            const res = await api.deleteMemory(id, scopeParam, groupParam);
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

  function attachMemberEvents(rootEl: HTMLElement) {
    rootEl.querySelectorAll<HTMLButtonElement>('[data-action="edit-member"]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const userId = btn.dataset.userid;
        if (!userId || !selectedGroupProfile) return;
        const member = selectedGroupProfile.members.find((m) => m.userId === userId);
        if (member) openEditMemberDialog(member);
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
    const scopeParam = currentScope;
    const groupParam = currentScope === 'group' ? selectedGroupId : '';

    for (const id of ids) {
      try {
        const res = await api.deleteMemory(id, scopeParam, groupParam);
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
    const isGroup = currentScope === 'group';
    const defaultOwner = isGroup ? 'group' : 'webui';
    const ownerHelp = isGroup
      ? '个人第一人称声明请填写群员 QQ；规则、第三人陈述与公共事实请填写 group'
      : '专属用户的 QQ 号或唯一标识';

    openDialog({
      title: isGroup ? '添加群聊记忆' : '添加私聊记忆',
      maxWidth: '32rem',
      bodyHtml: `
        <div class="flex flex-col gap-3.5">
          <div class="form-group">
            <label class="form-label" for="add-mem-owner">归属者 (Owner)</label>
            <input id="add-mem-owner" class="input text-xs" placeholder="${escapeHtml(defaultOwner)}" value="${escapeHtml(defaultOwner)}" />
            <p class="text-[11px] text-muted mt-1">${escapeHtml(ownerHelp)}</p>
          </div>
          <div class="form-group">
            <label class="form-label" for="add-mem-content">内容 <span class="text-destructive">*</span></label>
            <textarea id="add-mem-content" class="textarea text-xs" rows="4" placeholder="输入需要记录的记忆内容..."></textarea>
          </div>
          <div class="form-group">
            <label class="form-label" for="add-mem-tags">标签（逗号分隔）</label>
            <input id="add-mem-tags" class="input text-xs" placeholder="例如: 爱好, 规矩, 别称" />
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

        cancelBtn.addEventListener('click', () => close());
        saveBtn.addEventListener('click', async () => {
          const content = contentInput.value.trim();
          if (!content) {
            toast.error('记忆内容不能为空');
            return;
          }
          const owner = ownerInput.value.trim() || defaultOwner;
          const tags = tagsInput.value
            .split(',')
            .map((t) => t.trim())
            .filter(Boolean);

          try {
            saveBtn.disabled = true;
            const scopeParam = currentScope;
            const groupParam = currentScope === 'group' ? selectedGroupId : '';
            const res = await api.addMemory(owner, content, tags, scopeParam, groupParam);
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
              <span class="text-muted">作用域:</span>
              <span class="ml-1 font-medium text-foreground">${escapeHtml(mem.scope === 'group' ? `群聊 (${mem.groupId || '-'})` : '私聊')}</span>
            </div>
            <div>
              <span class="text-muted">归属者:</span>
              <span class="ml-1 font-medium text-foreground">${escapeHtml(mem.owner === 'group' ? '群整体 (group)' : mem.owner)}</span>
            </div>
            <div>
              <span class="text-muted">来源:</span>
              <span class="ml-1 text-foreground">${escapeHtml(getSourceLabel(mem.source))}</span>
            </div>
            <div>
              <span class="text-muted">召回次数:</span>
              <span class="ml-1 font-mono text-foreground">${escapeHtml(formatCount(mem.accessCount))}</span>
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

          ${
            mem.evidence
              ? `
            <div class="p-3 rounded-md border border-border bg-muted/60 text-xs flex flex-col gap-1.5">
              <div class="flex items-center justify-between text-muted">
                <span class="font-medium flex items-center gap-1 text-foreground">
                  ${icon('message_square_quote', 'w-3.5 h-3.5 text-muted')}
                  <span>权威引述片段 (Evidence)</span>
                </span>
                ${
                  mem.source === 'manual' && mem.evidence !== mem.content
                    ? '<span class="badge badge-secondary text-[10px] px-1.5 py-0">已人工修订</span>'
                    : ''
                }
              </div>
              <div class="font-mono text-foreground italic bg-background p-2 rounded border border-border/50 select-all break-words">${escapeHtml(
                mem.evidence,
              )}</div>
              <div class="flex items-center justify-between text-[11px] text-muted flex-wrap gap-1">
                ${
                  mem.sourceSenderId
                    ? `<span>发言人: <strong class="font-mono text-foreground">${escapeHtml(mem.sourceSenderId)}</strong></span>`
                    : ''
                }
                ${
                  mem.sourceMessageId
                    ? `<span>消息 ID: <span class="font-mono">${escapeHtml(mem.sourceMessageId)}</span></span>`
                    : ''
                }
              </div>
            </div>
          `
              : ''
          }

          ${
            mem.summary
              ? `
            <div class="text-xs text-muted bg-muted/40 p-2.5 rounded border border-border/40">
              <span class="font-medium">展示摘要:</span>
              <span class="text-foreground ml-1">${escapeHtml(mem.summary)}</span>
            </div>
          `
              : ''
          }

          <div class="form-group">
            <div class="flex items-center justify-between mb-1">
              <label class="form-label mb-0" for="edit-mem-content">内容</label>
              ${
                mem.evidence && mem.source === 'manual' && mem.evidence !== mem.content
                  ? '<span class="text-[11px] text-muted">（已人工修订，保留原始引述用于审计追溯）</span>'
                  : ''
              }
            </div>
            <textarea id="edit-mem-content" class="textarea text-xs" rows="4">${escapeHtml(mem.content)}</textarea>
          </div>

          <div class="form-group">
            <label class="form-label" for="edit-mem-tags">标签（逗号分隔）</label>
            <input id="edit-mem-tags" class="input text-xs" value="${escapeHtml((mem.tags || []).join(', '))}" />
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

          try {
            saveBtn.disabled = true;
            const scopeParam = currentScope;
            const groupParam = currentScope === 'group' ? selectedGroupId : '';
            const res = await api.updateMemory(mem.id, content, tags, scopeParam, groupParam);
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

  function openEditGroupNameDialog(currentName: string) {
    openDialog({
      title: `修改群名称 · ${selectedGroupId}`,
      maxWidth: '28rem',
      bodyHtml: `
        <div class="form-group">
          <label class="form-label" for="edit-group-name-input">群名称</label>
          <input id="edit-group-name-input" class="input text-xs" value="${escapeHtml(currentName)}" placeholder="输入群聊显示名称..." />
          <p class="text-[11px] text-muted mt-1">群名称有助于在管理面板与记忆检索中标识群聊。</p>
        </div>
      `,
      footerHtml: `
        <button class="btn btn-outline btn-sm" id="edit-group-cancel">取消</button>
        <button class="btn btn-primary btn-sm" id="edit-group-save">保存</button>
      `,
      onMount: (dialogEl, close) => {
        const cancelBtn = dialogEl.querySelector('#edit-group-cancel')!;
        const saveBtn = dialogEl.querySelector<HTMLButtonElement>('#edit-group-save')!;
        const nameInput = dialogEl.querySelector<HTMLInputElement>('#edit-group-name-input')!;

        cancelBtn.addEventListener('click', () => close());
        saveBtn.addEventListener('click', async () => {
          const newName = nameInput.value.trim();
          if (!newName) {
            toast.error('群名称不能为空');
            return;
          }
          try {
            saveBtn.disabled = true;
            const res = await api.updateGroupProfile(selectedGroupId, newName);
            if (res.success) {
              toast.success('群名称已更新');
              close();
              void loadGroupsAndData();
            } else {
              toast.error('修改失败: ' + res.error);
            }
          } catch (err) {
            toast.error('修改失败: ' + (err instanceof Error ? err.message : String(err)));
          } finally {
            saveBtn.disabled = false;
          }
        });
      },
    });
  }

  function openEditMemberDialog(member: MemberProfile) {
    openDialog({
      title: `编辑成员称呼 · ${member.nickname || member.userId}`,
      maxWidth: '32rem',
      bodyHtml: `
        <div class="flex flex-col gap-3.5">
          <div class="grid grid-cols-2 gap-2 p-3 rounded-md border border-border bg-muted text-xs">
            <div>
              <span class="text-muted">群员 QQ:</span>
              <span class="font-mono ml-1 font-medium text-foreground">${escapeHtml(member.userId)}</span>
            </div>
            <div>
              <span class="text-muted">群昵称:</span>
              <span class="ml-1 text-foreground">${escapeHtml(member.nickname || '-')}</span>
            </div>
            <div>
              <span class="text-muted">群名片:</span>
              <span class="ml-1 text-foreground" title="群名片仅用于身份识别，不作为称呼">${escapeHtml(member.card || '-')}</span>
            </div>
            <div>
              <span class="text-muted">群身份:</span>
              <span class="ml-1 text-foreground">${escapeHtml(member.role)}</span>
            </div>
            <div class="col-span-2 pt-1 border-t border-border/50">
              <span class="text-muted">当前生效称呼:</span>
              <span class="ml-1 font-semibold text-primary">${escapeHtml(member.callingName || '群友')}</span>
              <span class="text-[11px] text-muted ml-1">(解析优先级: 优先称呼 > 昵称 > 群友)</span>
            </div>
          </div>

          <div class="form-group">
            <label class="form-label" for="edit-member-pref">优先称呼 (Preferred Name)</label>
            <input id="edit-member-pref" class="input text-xs" value="${escapeHtml(member.preferredName || '')}" placeholder="例如: 霜霜、小明" />
            <p class="text-[11px] text-muted mt-1">最高优先级称呼。留空时将自动退化至群昵称或“群友”，群名片绝不会被用作称呼。</p>
          </div>

          <div class="form-group">
            <label class="form-label" for="edit-member-aliases">别名 (Aliases，逗号分隔)</label>
            <input id="edit-member-aliases" class="input text-xs" value="${escapeHtml((member.aliases || []).join(', '))}" placeholder="例如: 狐狸, 霜降, 呆毛" />
            <p class="text-[11px] text-muted mt-1">该群员的其他常见称呼与绰号，用于模型在群聊中识别对话主体。</p>
          </div>
        </div>
      `,
      footerHtml: `
        <button class="btn btn-outline btn-sm" id="edit-member-cancel">取消</button>
        <button class="btn btn-primary btn-sm" id="edit-member-save">保存称呼</button>
      `,
      onMount: (dialogEl, close) => {
        const cancelBtn = dialogEl.querySelector('#edit-member-cancel')!;
        const saveBtn = dialogEl.querySelector<HTMLButtonElement>('#edit-member-save')!;
        const prefInput = dialogEl.querySelector<HTMLInputElement>('#edit-member-pref')!;
        const aliasesInput = dialogEl.querySelector<HTMLInputElement>('#edit-member-aliases')!;

        cancelBtn.addEventListener('click', () => close());
        saveBtn.addEventListener('click', async () => {
          const preferredName = prefInput.value.trim();
          const aliases = aliasesInput.value
            .split(',')
            .map((a) => a.trim())
            .filter(Boolean);

          try {
            saveBtn.disabled = true;
            const res = await api.updateMemberProfile(
              selectedGroupId,
              member.userId,
              preferredName,
              aliases,
            );
            if (res.success) {
              toast.success('成员称呼已更新');
              close();
              void loadGroupProfileAndMemories();
            } else {
              toast.error('保存失败: ' + res.error);
            }
          } catch (err) {
            toast.error('保存失败: ' + (err instanceof Error ? err.message : String(err)));
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
    const reflectBtn = container.querySelector<HTMLButtonElement>('#memory-reflect-btn');
    const reflectIcon = container.querySelector<HTMLElement>('#memory-reflect-icon');
    if (reflectBtn) reflectBtn.disabled = true;
    if (reflectIcon) {
      reflectIcon.innerHTML = `<span class="spinner inline-block" style="width: 0.875rem; height: 0.875rem;"></span>`;
    }

    try {
      const scopeParam = currentScope;
      const groupParam = currentScope === 'group' ? selectedGroupId : '';
      const result = await api.triggerMemoryReflection(ownerFilter, scopeParam, groupParam);
      if (result.error) {
        toast.error('启动反思失败: ' + result.error);
        return;
      }
      if (!result.started) {
        toast.warning('已有记忆反思任务正在后台运行');
        return;
      }
      const targetLabel =
        currentScope === 'group'
          ? `群聊 ${selectedGroupId}`
          : ownerFilter
            ? `用户 ${ownerFilter}`
            : '全部私聊用户';
      toast.success(`已在后台启动 ${targetLabel} 的记忆反思`);
    } catch (err) {
      toast.error('启动反思失败: ' + (err instanceof Error ? err.message : String(err)));
    } finally {
      reflecting = false;
      if (reflectBtn) reflectBtn.disabled = false;
      if (reflectIcon) reflectIcon.innerHTML = icon('brain');
    }
  }

  // Export JSON
  async function exportMemories() {
    try {
      const scopeParam = currentScope;
      const groupParam = currentScope === 'group' ? selectedGroupId : '';
      const res = await api.exportMemories(scopeParam, groupParam);
      if (res.error) {
        toast.error('导出失败: ' + res.error);
        return;
      }
      const blob = new Blob([res.jsonContent], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      const suffix = currentScope === 'group' ? `group-${selectedGroupId}` : 'private';
      a.download = `memories-export-${suffix}-${new Date().toISOString().slice(0, 10)}.json`;
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
      if (isUnmounted) return;
      try {
        const scopeParam = currentScope;
        const groupParam = currentScope === 'group' ? selectedGroupId : '';
        const res = await api.importMemories(reader.result as string, false, scopeParam, groupParam);
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

  // Initial load
  renderSkeleton();
  void loadStats();
  void loadMemories();

  return () => {
    isUnmounted = true;
  };
}
