import {
  actionsCatAPI,
  type ActionsCatAction,
  type ActionsCatRun,
  type ActionsCatStatus,
} from '../api/client';
import { escapeHtml, formatDateTime } from '../utils/formatters';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { openDialog } from '../components/dialog';
import { copyToClipboard } from '../utils/clipboard';

export function mountActionsCatPage(container: HTMLElement): () => void {
  let isDisposed = false;
  let status: ActionsCatStatus | null = null;
  let actions: ActionsCatAction[] = [];
  let runs: ActionsCatRun[] = [];
  let selectedActionFilter = '';
  let loadingStatus = false;
  let loadingActions = false;
  let loadingRuns = false;

  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="flex items-center justify-between gap-4 flex-wrap pb-1">
        <div>
          <h1 class="page-title">ActionsCat 自动化</h1>
          <p class="page-description">管理与监控 ActionsCat 无服务器沙箱动作与执行历史，并向 Agent 开放自动化工具</p>
        </div>
        <div class="flex items-center gap-2 flex-wrap">
          <button class="btn btn-outline" id="actionscat-refresh-btn" title="刷新状态与动作列表">
            <span class="inline-flex" id="actionscat-refresh-icon">${icon('refresh')}</span>
            <span>刷新</span>
          </button>
          <button class="btn btn-primary" id="actionscat-dispatch-btn" title="发送测试事件到 ActionsCat">
            ${icon('play')}
            <span>触发事件</span>
          </button>
        </div>
      </header>

      <!-- Status & Endpoint Banner -->
      <div id="actionscat-status-container">
        <div class="card p-6 text-center text-muted">
          <span class="spinner"></span>
          <span class="ml-2">正在检查 ActionsCat 连接状态...</span>
        </div>
      </div>

      <!-- Agent Tools Info Card -->
      <section class="card p-5">
        <div class="flex items-center justify-between gap-4 flex-wrap mb-3">
          <div class="flex items-center gap-2">
            <span class="text-primary inline-flex">${icon('bot')}</span>
            <h2 class="text-base font-bold text-foreground">Agent 智能体自动化工具</h2>
          </div>
          <span class="badge badge-success inline-flex items-center gap-1.5" id="actionscat-tools-badge">
            <span class="inline-block w-2 h-2 rounded-full bg-emerald-500"></span>
            <span>已注册至 ToolRegistry</span>
          </span>
        </div>
        <p class="text-xs text-muted-foreground leading-relaxed mb-4">
          ActionsCat 深度集成于 FrostAgent 智能体核心工具链。当 Agent 进行对话推理时，可自动按需自主调用下列工具发现动作、执行任务并检查输出：
        </p>
        <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          <div class="p-3.5 rounded-md border border-border bg-secondary/40 flex flex-col gap-1.5">
            <div class="flex items-center gap-1.5">
              <span class="font-mono font-bold text-xs text-foreground">actionscat_list_actions</span>
            </div>
            <p class="text-xs text-muted-foreground leading-relaxed">
              查询可用 Actions 清单，包含 ID、名称、功能描述、启用状态及 runnable（是否具备有效构建）状态。
            </p>
          </div>
          <div class="p-3.5 rounded-md border border-border bg-secondary/40 flex flex-col gap-1.5">
            <div class="flex items-center gap-1.5">
              <span class="font-mono font-bold text-xs text-foreground">actionscat_create_action</span>
              <span class="badge badge-outline text-[10px] py-0 px-1">Admin</span>
            </div>
            <p class="text-xs text-muted-foreground leading-relaxed">
              动态注册新的 Action 动作元数据壳，支持配置名称、描述及最大并发数。
            </p>
          </div>
          <div class="p-3.5 rounded-md border border-border bg-secondary/40 flex flex-col gap-1.5">
            <div class="flex items-center gap-1.5">
              <span class="font-mono font-bold text-xs text-foreground">actionscat_create_version</span>
              <span class="badge badge-outline text-[10px] py-0 px-1">Admin</span>
            </div>
            <p class="text-xs text-muted-foreground leading-relaxed">
              创建不可变代码版本，支持源码映射 (files)、构建/运行规约、状态注入及运行时能力声明。
            </p>
          </div>
          <div class="p-3.5 rounded-md border border-border bg-secondary/40 flex flex-col gap-1.5">
            <div class="flex items-center gap-1.5">
              <span class="font-mono font-bold text-xs text-foreground">actionscat_build_version</span>
              <span class="badge badge-outline text-[10px] py-0 px-1">Admin</span>
            </div>
            <p class="text-xs text-muted-foreground leading-relaxed">
              触发沙箱编译打包。严格检查构建状态；若失败返回错误日志，且由于版本不可变需新建版本。
            </p>
          </div>
          <div class="p-3.5 rounded-md border border-border bg-secondary/40 flex flex-col gap-1.5">
            <div class="flex items-center gap-1.5">
              <span class="font-mono font-bold text-xs text-foreground">actionscat_activate_build</span>
              <span class="badge badge-outline text-[10px] py-0 px-1">Admin</span>
            </div>
            <p class="text-xs text-muted-foreground leading-relaxed">
              将成功编译的构建 (succeeded) 激活为 Action 的生效运行版本，使其进入可运行 (runnable) 状态。
            </p>
          </div>
          <div class="p-3.5 rounded-md border border-border bg-secondary/40 flex flex-col gap-1.5">
            <div class="flex items-center gap-1.5">
              <span class="font-mono font-bold text-xs text-foreground">actionscat_deploy_action</span>
              <span class="badge badge-primary text-[10px] py-0 px-1">All-in-One</span>
            </div>
            <p class="text-xs text-muted-foreground leading-relaxed">
              一站式部署生命周期：创建版本 -> 触发编译 -> 校验状态 -> 激活成功构建，自动化就绪 runnable 动作。
            </p>
          </div>
          <div class="p-3.5 rounded-md border border-border bg-secondary/40 flex flex-col gap-1.5">
            <div class="flex items-center gap-1.5">
              <span class="font-mono font-bold text-xs text-foreground">actionscat_run_action</span>
            </div>
            <p class="text-xs text-muted-foreground leading-relaxed">
              触发已激活构建的 Action 执行，支持注入环境变量；支持同步等待完成或异步返回 run_id。
            </p>
          </div>
          <div class="p-3.5 rounded-md border border-border bg-secondary/40 flex flex-col gap-1.5">
            <div class="flex items-center gap-1.5">
              <span class="font-mono font-bold text-xs text-foreground">actionscat_get_run</span>
            </div>
            <p class="text-xs text-muted-foreground leading-relaxed">
              查询执行记录状态、退出码、耗时及 stdout/stderr 日志，敏感环境变量已脱敏保护。
            </p>
          </div>
          <div class="p-3.5 rounded-md border border-border bg-secondary/40 flex flex-col gap-1.5">
            <div class="flex items-center gap-1.5">
              <span class="font-mono font-bold text-xs text-foreground">actionscat_get_build</span>
            </div>
            <p class="text-xs text-muted-foreground leading-relaxed">
              查询构建任务状态、制品哈希及详细编译输出日志，用于超时排查或分析编译错误。
            </p>
          </div>
          <div class="p-3.5 rounded-md border border-border bg-secondary/40 flex flex-col gap-1.5">
            <div class="flex items-center gap-1.5">
              <span class="font-mono font-bold text-xs text-foreground">actionscat_list_builds</span>
            </div>
            <p class="text-xs text-muted-foreground leading-relaxed">
              查询 Action 的历史构建列表，支持过滤指定版本；用于构建超时或未决状态的幂等恢复与发现。
            </p>
          </div>
        </div>
      </section>

      <!-- Actions List Section -->
      <section class="flex flex-col gap-3">
        <div class="flex items-center justify-between gap-4 flex-wrap">
          <div class="flex items-center gap-2">
            <h2 class="text-base font-bold text-foreground">注册动作 (Actions)</h2>
            <span class="badge badge-secondary" id="actionscat-actions-count">0</span>
          </div>
          <button class="btn btn-outline btn-sm" id="actionscat-create-action-btn" title="新建 Action 自动化动作">
            ${icon('plus', 'size-3.5')}
            <span>新建动作</span>
          </button>
        </div>
        <div id="actionscat-actions-container">
          <div class="card p-8 text-center text-muted">
            <span class="spinner"></span>
            <span class="ml-2">正在获取已注册动作...</span>
          </div>
        </div>
      </section>

      <!-- Execution Runs Section -->
      <section class="flex flex-col gap-3">
        <div class="flex items-center justify-between gap-4 flex-wrap">
          <div class="flex items-center gap-2">
            <h2 class="text-base font-bold text-foreground">执行记录 (Runs)</h2>
            <span class="badge badge-secondary" id="actionscat-runs-count">0</span>
          </div>
          <div class="flex items-center gap-2 flex-wrap">
            <select class="input text-xs" id="actionscat-filter-action" style="height: 2rem; min-width: 12rem;">
              <option value="">全部动作</option>
            </select>
            <button class="btn btn-outline btn-sm" id="actionscat-refresh-runs-btn" title="刷新运行记录">
              ${icon('refresh', 'size-3')}
              <span>刷新记录</span>
            </button>
          </div>
        </div>
        <div id="actionscat-runs-container">
          <div class="card p-8 text-center text-muted">
            <span class="spinner"></span>
            <span class="ml-2">正在获取执行记录...</span>
          </div>
        </div>
      </section>
    </div>

    <style>
      .terminal-output-box {
        background-color: #0d1117;
        color: #c9d1d9;
        font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
        font-size: 0.8125rem;
        line-height: 1.5;
        padding: 0.875rem 1rem;
        border-radius: var(--radius-md);
        border: 1px solid #30363d;
        max-height: 24rem;
        overflow-y: auto;
        white-space: pre-wrap;
        word-break: break-all;
      }
      .terminal-output-box.stderr {
        color: #ff7b72;
      }
      .action-item-card {
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        background-color: var(--card);
        padding: 1rem 1.25rem;
        display: flex;
        flex-direction: column;
        gap: 0.75rem;
        transition: border-color 0.15s ease;
      }
      .action-item-card:hover {
        border-color: var(--muted-foreground);
      }
    </style>
  `;

  const statusContainer = container.querySelector<HTMLElement>('#actionscat-status-container')!;
  const actionsContainer = container.querySelector<HTMLElement>('#actionscat-actions-container')!;
  const runsContainer = container.querySelector<HTMLElement>('#actionscat-runs-container')!;
  const actionsCountEl = container.querySelector<HTMLElement>('#actionscat-actions-count')!;
  const runsCountEl = container.querySelector<HTMLElement>('#actionscat-runs-count')!;
  const actionFilterSelect = container.querySelector<HTMLSelectElement>('#actionscat-filter-action')!;
  const toolsBadgeEl = container.querySelector<HTMLElement>('#actionscat-tools-badge')!;

  // Load Status
  async function loadStatus(): Promise<void> {
    if (loadingStatus) return;
    loadingStatus = true;
    try {
      status = await actionsCatAPI.getStatus();
      if (isDisposed) return;
      renderStatus();
    } catch (err: unknown) {
      if (isDisposed) return;
      const msg = err instanceof Error ? err.message : String(err);
      status = {
        configured: false,
        healthy: false,
        endpoint: '',
        error: msg,
      };
      renderStatus();
    } finally {
      loadingStatus = false;
    }
  }

  // Render Status
  function renderStatus(): void {
    if (!status) return;

    if (!status.configured) {
      toolsBadgeEl.className = 'badge badge-warning inline-flex items-center gap-1.5';
      toolsBadgeEl.innerHTML = `
        <span class="inline-block w-2 h-2 rounded-full bg-amber-500"></span>
        <span>待配置端点后激活</span>
      `;
      statusContainer.innerHTML = `
        <div class="card p-5 border-amber-500/30 bg-amber-500/10 flex flex-col md:flex-row items-start md:items-center justify-between gap-4">
          <div class="flex items-start gap-3">
            <span class="text-amber-500 inline-flex mt-0.5">${icon('circle_alert', 'size-5')}</span>
            <div>
              <h3 class="text-sm font-bold text-foreground">ActionsCat 服务尚未配置</h3>
              <p class="text-xs text-muted-foreground mt-1 leading-relaxed">
                当前实例尚未设置 <code class="px-1 py-0.5 rounded bg-muted font-mono">ACTIONSCAT_ENDPOINT</code> 环境变量。配置后即可启用动作执行与沙箱联动。
              </p>
            </div>
          </div>
          <a href="#/settings/backend" class="btn btn-outline btn-sm whitespace-nowrap">
            ${icon('settings', 'size-3.5')}
            <span>前往服务端配置</span>
          </a>
        </div>
      `;
      return;
    }

    if (!status.healthy) {
      toolsBadgeEl.className = 'badge badge-destructive inline-flex items-center gap-1.5';
      toolsBadgeEl.innerHTML = `
        <span class="inline-block w-2 h-2 rounded-full bg-red-500"></span>
        <span>连接异常</span>
      `;
      statusContainer.innerHTML = `
        <div class="card p-5 border-destructive/30 bg-destructive/10 flex flex-col md:flex-row items-start md:items-center justify-between gap-4">
          <div class="flex items-start gap-3">
            <span class="text-destructive inline-flex mt-0.5">${icon('circle_alert', 'size-5')}</span>
            <div>
              <h3 class="text-sm font-bold text-foreground">ActionsCat 健康检查未通过</h3>
              <p class="text-xs text-muted-foreground mt-1 leading-relaxed">
                端点: <code class="px-1 py-0.5 rounded bg-muted font-mono">${escapeHtml(status.endpoint)}</code> · 错误: ${escapeHtml(status.error || '健康检查请求失败')}
              </p>
            </div>
          </div>
          <button class="btn btn-outline btn-sm" id="actionscat-recheck-btn">
            ${icon('refresh', 'size-3.5')}
            <span>重新检查</span>
          </button>
        </div>
      `;
      statusContainer.querySelector('#actionscat-recheck-btn')?.addEventListener('click', () => {
        void loadAll();
      });
      return;
    }

    if (status.authenticated === false) {
      toolsBadgeEl.className = 'badge badge-warning inline-flex items-center gap-1.5';
      toolsBadgeEl.innerHTML = `
        <span class="inline-block w-2 h-2 rounded-full bg-amber-500"></span>
        <span>未认证/Token无效</span>
      `;
      statusContainer.innerHTML = `
        <div class="card p-5 border-amber-500/30 bg-amber-500/10 flex flex-col md:flex-row items-start md:items-center justify-between gap-4">
          <div class="flex items-start gap-3">
            <span class="text-amber-500 inline-flex mt-0.5">${icon('lock', 'size-5')}</span>
            <div>
              <h3 class="text-sm font-bold text-foreground">ActionsCat 管理凭据认证未通过</h3>
              <p class="text-xs text-muted-foreground mt-1 leading-relaxed">
                服务已连通，但管理 API 认证失败。请检查 <code>ACTIONSCAT_MANAGEMENT_TOKEN</code> 是否正确。<br>
                ${escapeHtml(status.error || '401 Unauthorized')}
              </p>
            </div>
          </div>
          <div class="flex items-center gap-2">
            <a href="#/settings/backend" class="btn btn-outline btn-sm whitespace-nowrap">
              ${icon('settings', 'size-3.5')}
              <span>配置 Token</span>
            </a>
            <button class="btn btn-outline btn-sm" id="actionscat-recheck-btn">
              ${icon('refresh', 'size-3.5')}
              <span>重新检查</span>
            </button>
          </div>
        </div>
      `;
      statusContainer.querySelector('#actionscat-recheck-btn')?.addEventListener('click', () => {
        void loadAll();
      });
      return;
    }

    // Configured & Healthy & Authenticated
    toolsBadgeEl.className = 'badge badge-success inline-flex items-center gap-1.5';
    toolsBadgeEl.innerHTML = `
      <span class="inline-block w-2 h-2 rounded-full bg-emerald-500"></span>
      <span>已连接并就绪</span>
    `;
    statusContainer.innerHTML = `
      <div class="card p-4 flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3">
        <div class="flex items-center gap-3">
          <span class="inline-flex p-2 rounded-full bg-emerald-500/10 text-emerald-500">
            ${icon('circle_check', 'size-5')}
          </span>
          <div>
            <div class="flex items-center gap-2 flex-wrap">
              <span class="text-sm font-bold text-foreground">ActionsCat 核心服务已连接</span>
              <span class="badge badge-success text-xs">Healthy</span>
            </div>
            <div class="flex items-center gap-1.5 text-xs text-muted-foreground mt-0.5 font-mono">
              <span>端点: ${escapeHtml(status.endpoint)}</span>
              <button
                class="btn btn-ghost btn-icon-sm"
                style="width: 1.25rem; height: 1.25rem;"
                id="actionscat-copy-endpoint-btn"
                title="复制端点地址"
              >
                ${icon('copy', 'size-3')}
              </button>
            </div>
          </div>
        </div>
        <div class="flex items-center gap-2 self-stretch sm:self-auto justify-end">
          <span class="text-xs text-muted-foreground">共 ${actions.length} 个动作</span>
        </div>
      </div>
    `;

    statusContainer.querySelector('#actionscat-copy-endpoint-btn')?.addEventListener('click', async () => {
      if (status?.endpoint) {
        await copyToClipboard(status.endpoint);
        toast.success('已复制端点地址');
      }
    });
  }

  // Load Actions
  async function loadActions(): Promise<void> {
    if (loadingActions) return;
    loadingActions = true;
    try {
      if (!status?.configured) {
        actions = [];
        renderActions();
        return;
      }
      actions = await actionsCatAPI.listActions();
      if (isDisposed) return;
      renderActions();
      updateFilterOptions();
    } catch (err: unknown) {
      if (isDisposed) return;
      const msg = err instanceof Error ? err.message : String(err);
      actionsContainer.innerHTML = `
        <div class="card p-6 text-center text-muted">
          <span class="text-destructive">${escapeHtml(msg)}</span>
        </div>
      `;
    } finally {
      loadingActions = false;
    }
  }

  // Render Actions
  function renderActions(): void {
    actionsCountEl.textContent = String(actions.length);

    if (actions.length === 0) {
      actionsContainer.innerHTML = `
        <div class="card p-8 text-center text-muted flex flex-col items-center justify-center gap-2">
          <span class="inline-flex text-muted-foreground">${icon('play', 'size-8')}</span>
          <p class="text-sm font-medium">当前暂无已注册的 Action 动作</p>
          <p class="text-xs text-muted-foreground">在 ActionsCat 后端配置 action 声明后，将自动在此处展示并供 Agent 调用</p>
        </div>
      `;
      return;
    }

    actionsContainer.innerHTML = `
      <div class="grid grid-cols-1 gap-3">
        ${actions
          .map((act) => {
            const caps = act.capabilities || [];
            return `
              <div class="action-item-card">
                <div class="flex items-start justify-between gap-3 flex-wrap">
                  <div class="flex flex-col gap-1">
                    <div class="flex items-center gap-2 flex-wrap">
                      <span class="font-bold text-sm text-foreground">${escapeHtml(act.name || act.id)}</span>
                      <code class="text-xs text-muted-foreground px-1.5 py-0.5 rounded bg-secondary font-mono">${escapeHtml(act.id)}</code>
                      <span class="badge ${
                        act.enabled
                          ? act.active_build_id
                            ? 'badge-success'
                            : 'badge-warning'
                          : 'badge-outline'
                      } text-xs">
                        ${
                          act.enabled
                            ? act.active_build_id
                              ? '可运行'
                              : '未构建/未激活'
                            : '已禁用'
                        }
                      </span>
                    </div>
                    <p class="text-xs text-muted-foreground leading-relaxed mt-0.5">
                      ${escapeHtml(act.description || '无动作描述')}
                    </p>
                  </div>

                  <div class="flex items-center gap-2">
                    <button
                      class="btn btn-primary btn-sm"
                      data-action="trigger-action"
                      data-id="${escapeHtml(act.id)}"
                      data-name="${escapeHtml(act.name || act.id)}"
                    >
                      ${icon('play', 'size-3')}
                      <span>手动执行</span>
                    </button>
                    <button
                      class="btn btn-outline btn-sm"
                      data-action="view-action-runs"
                      data-id="${escapeHtml(act.id)}"
                    >
                      ${icon('time', 'size-3')}
                      <span>运行历史</span>
                    </button>
                  </div>
                </div>

                <div class="flex items-center gap-3 flex-wrap text-xs text-muted-foreground pt-1 border-t border-border">
                  ${
                    act.active_version_id
                      ? `
                    <div class="flex items-center gap-1 font-mono">
                      <span>版本: ${escapeHtml(act.active_version_id)}</span>
                    </div>
                  `
                      : '<span class="text-amber-500 font-mono">未激活版本</span>'
                  }
                  ${
                    act.active_build_id
                      ? `
                    <div class="flex items-center gap-1 font-mono">
                      <span>构建: ${escapeHtml(act.active_build_id)}</span>
                    </div>
                  `
                      : '<span class="text-amber-500 font-mono">未关联构建</span>'
                  }
                  ${
                    act.schedule
                      ? `
                    <div class="flex items-center gap-1 font-mono">
                      ${icon('clock', 'size-3')}
                      <span>定时: ${escapeHtml(act.schedule)}</span>
                    </div>
                  `
                      : ''
                  }
                  ${
                    act.timeout_sec
                      ? `
                    <div class="flex items-center gap-1 font-mono">
                      ${icon('schedule', 'size-3')}
                      <span>超时: ${act.timeout_sec}s</span>
                    </div>
                  `
                      : ''
                  }
                  ${
                    caps.length > 0
                      ? `
                    <div class="flex items-center gap-1.5 flex-wrap">
                      <span>权限:</span>
                      ${caps
                        .map(
                          (c) =>
                            `<span class="badge badge-purple text-xs font-mono">${escapeHtml(c)}</span>`,
                        )
                        .join('')}
                    </div>
                  `
                      : '<span class="text-muted">无特殊权限声明</span>'
                  }
                </div>
              </div>
            `;
          })
          .join('')}
      </div>
    `;

    // Attach Action Events
    actionsContainer.querySelectorAll('[data-action="trigger-action"]').forEach((btn) => {
      btn.addEventListener('click', (e) => {
        const target = e.currentTarget as HTMLElement;
        const actID = target.dataset.id;
        const actName = target.dataset.name;
        if (actID) {
          openTriggerDialog(actID, actName || actID);
        }
      });
    });

    actionsContainer.querySelectorAll('[data-action="view-action-runs"]').forEach((btn) => {
      btn.addEventListener('click', (e) => {
        const target = e.currentTarget as HTMLElement;
        const actID = target.dataset.id;
        if (actID) {
          actionFilterSelect.value = actID;
          selectedActionFilter = actID;
          void loadRuns();
        }
      });
    });
  }

  // Update Filter Options
  function updateFilterOptions(): void {
    const curVal = actionFilterSelect.value;
    actionFilterSelect.innerHTML = `
      <option value="">全部动作</option>
      ${actions
        .map(
          (act) => `
        <option value="${escapeHtml(act.id)}" ${curVal === act.id ? 'selected' : ''}>
          ${escapeHtml(act.name ? `${act.name} (${act.id})` : act.id)}
        </option>
      `,
        )
        .join('')}
    `;
  }

  // Load Runs
  async function loadRuns(): Promise<void> {
    if (loadingRuns) return;
    loadingRuns = true;
    try {
      if (!status?.configured) {
        runs = [];
        renderRuns();
        return;
      }

      if (selectedActionFilter) {
        runs = await actionsCatAPI.listRuns(selectedActionFilter, 50, 0);
      } else {
        // Collect runs across actions
        const results = await Promise.all(
          actions.map(async (act) => {
            try {
              return await actionsCatAPI.listRuns(act.id, 20, 0);
            } catch {
              return [];
            }
          }),
        );
        runs = results
          .flat()
          .sort(
            (a, b) =>
              new Date(b.created_at).getTime() -
              new Date(a.created_at).getTime(),
          );
      }

      if (isDisposed) return;
      renderRuns();
    } catch (err: unknown) {
      if (isDisposed) return;
      const msg = err instanceof Error ? err.message : String(err);
      runsContainer.innerHTML = `
        <div class="card p-6 text-center text-muted">
          <span class="text-destructive">${escapeHtml(msg)}</span>
        </div>
      `;
    } finally {
      loadingRuns = false;
    }
  }

  // Render Runs
  function renderRuns(): void {
    runsCountEl.textContent = String(runs.length);

    if (runs.length === 0) {
      runsContainer.innerHTML = `
        <div class="card p-8 text-center text-muted flex flex-col items-center justify-center gap-2">
          <span class="inline-flex text-muted-foreground">${icon('time', 'size-8')}</span>
          <p class="text-sm font-medium">暂无执行记录</p>
          <p class="text-xs text-muted-foreground">当动作被定时器、事件或 Agent 触发执行后，执行状态与日志将汇总展示在此处</p>
        </div>
      `;
      return;
    }

    runsContainer.innerHTML = `
      <div class="card table-card overflow-hidden">
        <div class="table-container">
          <table class="table text-xs">
            <thead>
              <tr>
                <th style="width: 10rem;">运行 ID</th>
                <th>关联动作</th>
                <th style="width: 6.5rem;">触发方式</th>
                <th style="width: 6rem;">状态</th>
                <th style="width: 5rem;">耗时</th>
                <th style="width: 11rem;">开始时间</th>
                <th style="width: 6.5rem; text-align: right;">操作</th>
              </tr>
            </thead>
            <tbody>
              ${runs
                .map((run) => {
                  let statusBadgeClass = 'badge-secondary';
                  let statusText = run.status;
                  switch (run.status) {
                    case 'succeeded':
                      statusBadgeClass = 'badge-success';
                      statusText = '成功';
                      break;
                    case 'failed':
                      statusBadgeClass = 'badge-destructive';
                      statusText = '失败';
                      break;
                    case 'running':
                      statusBadgeClass = 'badge-warning';
                      statusText = '运行中';
                      break;
                    case 'pending':
                      statusBadgeClass = 'badge-secondary';
                      statusText = '排队中';
                      break;
                  }

                  const durationDisplay =
                    run.duration_ms > 1000
                      ? `${(run.duration_ms / 1000).toFixed(2)}s`
                      : `${run.duration_ms}ms`;

                  return `
                    <tr>
                      <td>
                        <div class="flex items-center gap-1 font-mono">
                          <span>${escapeHtml(run.id.slice(0, 14))}</span>
                          <button
                            class="btn btn-ghost btn-icon-sm"
                            style="width: 1.25rem; height: 1.25rem;"
                            data-action="copy-run-id"
                            data-id="${escapeHtml(run.id)}"
                            title="复制完整运行ID"
                          >
                            ${icon('copy', 'size-3')}
                          </button>
                        </div>
                      </td>
                      <td>
                        <span class="font-mono text-foreground font-medium">${escapeHtml(run.action_id)}</span>
                      </td>
                      <td>
                        <span class="badge badge-outline text-xs">${escapeHtml(run.trigger_type || 'manual')}</span>
                      </td>
                      <td>
                        <span class="badge ${statusBadgeClass} text-xs">${escapeHtml(statusText)}</span>
                      </td>
                      <td class="font-mono text-muted-foreground">
                        ${escapeHtml(durationDisplay)}
                      </td>
                      <td class="text-muted-foreground whitespace-nowrap">
                        ${escapeHtml(formatDateTime(run.created_at))}
                      </td>
                      <td style="text-align: right;">
                        <button
                          class="btn btn-outline btn-sm"
                          data-action="view-run-logs"
                          data-action-id="${escapeHtml(run.action_id)}"
                          data-run-id="${escapeHtml(run.id)}"
                        >
                          ${icon('file_text', 'size-3')}
                          <span>日志</span>
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

    // Attach Runs Events
    runsContainer.querySelectorAll('[data-action="copy-run-id"]').forEach((btn) => {
      btn.addEventListener('click', async (e) => {
        const target = e.currentTarget as HTMLElement;
        const id = target.dataset.id;
        if (id) {
          await copyToClipboard(id);
          toast.success(`已复制运行 ID: ${id}`);
        }
      });
    });

    runsContainer.querySelectorAll('[data-action="view-run-logs"]').forEach((btn) => {
      btn.addEventListener('click', (e) => {
        const target = e.currentTarget as HTMLElement;
        const actionID = target.dataset.actionId;
        const runID = target.dataset.runId;
        if (actionID && runID) {
          openRunLogsDialog(actionID, runID);
        }
      });
    });
  }

  // Dialog: Manual Trigger Run
  function openTriggerDialog(actionID: string, actionName: string): void {
    const act = actions.find((a) => a.id === actionID);
    const isUnbuilt = act && (!act.active_build_id || !act.active_version_id);
    openDialog({
      title: `手动执行动作: ${actionName}`,
      description: `动作 ID: ${actionID}`,
      maxWidth: '34rem',
      bodyHtml: `
        <div class="flex flex-col gap-4">
          ${
            isUnbuilt
              ? `
            <div class="p-3 rounded bg-amber-500/10 border border-amber-500/30 text-amber-500 text-xs flex items-start gap-2">
              <span class="inline-flex mt-0.5">${icon('circle_alert', 'size-4')}</span>
              <div class="leading-relaxed">
                <strong class="font-semibold">提示：该动作尚未关联激活构建 (active_build_id 为空)</strong><br>
                在 ActionsCat 中绑定并激活构建产物前，执行将因缺少可运行镜像/二进制而失败。
              </div>
            </div>
          `
              : ''
          }
          <div class="form-group">
            <label class="form-label text-xs font-semibold">附加环境变量 (Extra Environment Variables)</label>
            <p class="text-xs text-muted-foreground mb-1.5">
              每行输入一个键值对，例如 <code class="font-mono">CITY=Tokyo</code> 或 <code class="font-mono">DEBUG=true</code>
            </p>
            <textarea
              id="trigger-extra-env"
              class="input font-mono text-xs w-full"
              rows="4"
              placeholder="KEY=VALUE&#10;CITY=Beijing"
            ></textarea>
          </div>

          <div class="form-group">
            <label class="form-label text-xs font-semibold">触发元数据 (Trigger Metadata JSON, 可选)</label>
            <textarea
              id="trigger-metadata-json"
              class="input font-mono text-xs w-full"
              rows="3"
              placeholder="{ &quot;source&quot;: &quot;dashboard&quot; }"
            ></textarea>
          </div>
        </div>
      `,
      footerHtml: `
        <button class="btn btn-outline" id="dialog-trigger-cancel">取消</button>
        <button class="btn btn-primary" id="dialog-trigger-submit">
          ${icon('play', 'size-3.5')}
          <span>立即触发</span>
        </button>
      `,
      onMount: (dialogEl, close) => {
        const envArea = dialogEl.querySelector<HTMLTextAreaElement>('#trigger-extra-env')!;
        const metaArea = dialogEl.querySelector<HTMLTextAreaElement>('#trigger-metadata-json')!;
        const cancelBtn = dialogEl.querySelector<HTMLButtonElement>('#dialog-trigger-cancel')!;
        const submitBtn = dialogEl.querySelector<HTMLButtonElement>('#dialog-trigger-submit')!;

        cancelBtn.addEventListener('click', () => close());

        submitBtn.addEventListener('click', async () => {
          submitBtn.disabled = true;
          submitBtn.innerHTML = `<span class="spinner inline-block" style="width:0.875rem;height:0.875rem"></span> <span>触发中...</span>`;

          try {
            // Parse extra_env
            const extraEnv: Record<string, string> = {};
            const envLines = envArea.value.split('\n');
            for (const line of envLines) {
              const trimmed = line.trim();
              if (!trimmed || trimmed.startsWith('#')) continue;
              const eqIdx = trimmed.indexOf('=');
              if (eqIdx > 0) {
                const k = trimmed.slice(0, eqIdx).trim();
                const v = trimmed.slice(eqIdx + 1).trim();
                if (k) extraEnv[k] = v;
              }
            }

            // Parse metadata
            let triggerMetadata: Record<string, string> | undefined;
            if (metaArea.value.trim()) {
              try {
                const parsed = JSON.parse(metaArea.value.trim());
                if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
                  throw new Error('元数据必须是 JSON 对象');
                }
                triggerMetadata = {};
                for (const [k, v] of Object.entries(parsed)) {
                  triggerMetadata[k] = typeof v === 'string' ? v : JSON.stringify(v);
                }
              } catch {
                toast.error('触发元数据 JSON 格式不合法');
                submitBtn.disabled = false;
                submitBtn.innerHTML = `${icon('play', 'size-3.5')} <span>立即触发</span>`;
                return;
              }
            }

            const run = await actionsCatAPI.triggerRun(actionID, {
              extra_env: Object.keys(extraEnv).length > 0 ? extraEnv : undefined,
              trigger_metadata: triggerMetadata,
            });

            toast.success(`动作已触发，运行 ID: ${run.id}`);
            close();
            await loadRuns();
          } catch (err: unknown) {
            const msg = err instanceof Error ? err.message : String(err);
            toast.error(`触发失败: ${msg}`);
            submitBtn.disabled = false;
            submitBtn.innerHTML = `${icon('play', 'size-3.5')} <span>立即触发</span>`;
          }
        });
      },
    });
  }

  // Dialog: View Run Logs
  async function openRunLogsDialog(actionID: string, runID: string): Promise<void> {
    openDialog({
      title: `运行输出与日志`,
      description: `动作: ${actionID} · 运行 ID: ${runID}`,
      maxWidth: '44rem',
      bodyHtml: `
        <div class="flex flex-col gap-3">
          <div class="flex items-center justify-between gap-2 flex-wrap">
            <div class="flex items-center gap-2" id="dialog-run-status-badge">
              <span class="spinner inline-block"></span>
              <span class="text-xs text-muted-foreground">正在加载运行日志...</span>
            </div>
            <button class="btn btn-outline btn-sm" id="dialog-copy-logs-btn">
              ${icon('copy', 'size-3')}
              <span>复制完整日志</span>
            </button>
          </div>

          <div class="flex flex-col gap-1.5">
            <span class="text-xs font-semibold text-foreground">标准输出 (STDOUT)</span>
            <div class="terminal-output-box" id="dialog-stdout-box">正在拉取...</div>
          </div>

          <div class="flex flex-col gap-1.5">
            <span class="text-xs font-semibold text-foreground">标准错误 (STDERR)</span>
            <div class="terminal-output-box stderr" id="dialog-stderr-box">正在拉取...</div>
          </div>
        </div>
      `,
      footerHtml: `
        <button class="btn btn-outline" id="dialog-logs-close">关闭</button>
      `,
      onMount: async (dialogEl, close) => {
        dialogEl.querySelector('#dialog-logs-close')?.addEventListener('click', () => close());
        const copyBtn = dialogEl.querySelector<HTMLButtonElement>('#dialog-copy-logs-btn');
        const stdoutBox = dialogEl.querySelector<HTMLElement>('#dialog-stdout-box')!;
        const stderrBox = dialogEl.querySelector<HTMLElement>('#dialog-stderr-box')!;
        const statusBadge = dialogEl.querySelector<HTMLElement>('#dialog-run-status-badge')!;

        try {
          const [runDetail, runLogs] = await Promise.all([
            actionsCatAPI.getRun(actionID, runID).catch(() => null),
            actionsCatAPI.getRunLogs(actionID, runID).catch(() => ({ stdout: '', stderr: '' })),
          ]);

          const stdoutText = runLogs.stdout || runDetail?.stdout || '';
          const stderrText = runLogs.stderr || runDetail?.stderr || '';

          stdoutBox.textContent = stdoutText || '(无标准输出)';
          stderrBox.textContent = stderrText || '(无错误输出)';

          let statusClass = 'badge-secondary';
          if (runDetail?.status === 'succeeded') statusClass = 'badge-success';
          if (runDetail?.status === 'failed') statusClass = 'badge-destructive';

          statusBadge.innerHTML = `
            <span class="badge ${statusClass} text-xs">${escapeHtml(runDetail?.status || 'unknown')}</span>
            <span class="text-xs text-muted-foreground font-mono">耗时: ${runDetail?.duration_ms ?? 0}ms</span>
            <span class="text-xs text-muted-foreground font-mono">退出码: ${runDetail?.exit_code ?? 0}</span>
          `;

          copyBtn?.addEventListener('click', async () => {
            const fullLog = `=== STDOUT ===\n${stdoutText}\n\n=== STDERR ===\n${stderrText}`;
            await copyToClipboard(fullLog);
            toast.success('已复制运行输出与日志');
          });
        } catch (err: unknown) {
          const msg = err instanceof Error ? err.message : String(err);
          stdoutBox.textContent = `加载日志失败: ${msg}`;
          stderrBox.textContent = '';
        }
      },
    });
  }

  // Dialog: Dispatch Event
  function openDispatchDialog(): void {
    openDialog({
      title: '向 ActionsCat 分发测试事件',
      description: '向 /api/v1/dispatch 发送任意 JSON 事件，触发匹配条件的自动化动作',
      maxWidth: '34rem',
      bodyHtml: `
        <div class="flex flex-col gap-3">
          <div class="form-group">
            <label class="form-label text-xs font-semibold">事件 Payload (JSON)</label>
            <textarea
              id="dispatch-payload-json"
              class="input font-mono text-xs w-full"
              rows="6"
            >{
  "event": "manual_test",
  "source": "frostagent_dashboard",
  "timestamp": "${new Date().toISOString()}"
}</textarea>
          </div>
        </div>
      `,
      footerHtml: `
        <button class="btn btn-outline" id="dialog-dispatch-cancel">取消</button>
        <button class="btn btn-primary" id="dialog-dispatch-submit">
          ${icon('play', 'size-3.5')}
          <span>发送事件</span>
        </button>
      `,
      onMount: (dialogEl, close) => {
        const payloadArea = dialogEl.querySelector<HTMLTextAreaElement>('#dispatch-payload-json')!;
        const cancelBtn = dialogEl.querySelector<HTMLButtonElement>('#dialog-dispatch-cancel')!;
        const submitBtn = dialogEl.querySelector<HTMLButtonElement>('#dialog-dispatch-submit')!;

        cancelBtn.addEventListener('click', () => close());

        submitBtn.addEventListener('click', async () => {
          submitBtn.disabled = true;
          try {
            const parsed = JSON.parse(payloadArea.value.trim());
            await actionsCatAPI.dispatch(parsed);
            toast.success('事件已成功分发至 ActionsCat');
            close();
            // Refresh runs shortly after
            setTimeout(() => {
              void loadRuns();
            }, 1000);
          } catch (err: unknown) {
            const msg = err instanceof Error ? err.message : String(err);
            toast.error(`发送事件失败: ${msg}`);
            submitBtn.disabled = false;
          }
        });
      },
    });
  }

  // Dialog: Create Action
  function openCreateActionDialog(): void {
    openDialog({
      title: '新建 Action 自动化动作',
      description: '在 ActionsCat 中注册动作元数据定义。新动作初始为元数据壳，需在 ActionsCat 中完成构建并激活版本后方可执行。',
      maxWidth: '30rem',
      bodyHtml: `
        <div class="flex flex-col gap-3">
          <div class="p-2.5 rounded bg-muted/50 text-xs text-muted-foreground border border-border leading-relaxed flex items-start gap-2">
            <span class="inline-flex text-muted-foreground mt-0.5">${icon('info', 'size-3.5')}</span>
            <span>提示：此处注册的是动作元数据声明。创建后需在 ActionsCat 中绑定代码版本并完成镜像/二进制构建（生成 active_build_id）后方可实际运行。</span>
          </div>
          <div class="form-group">
            <label class="form-label text-xs font-semibold">动作名称 <span class="text-destructive">*</span></label>
            <input
              type="text"
              id="new-action-name"
              class="input text-xs w-full"
              placeholder="例如: 每日数据汇总 / 自动备份"
              required
            />
          </div>
          <div class="form-group">
            <label class="form-label text-xs font-semibold">动作描述</label>
            <textarea
              id="new-action-description"
              class="input text-xs w-full"
              rows="3"
              placeholder="简要说明此动作的功能与触发场景..."
            ></textarea>
          </div>
          <div class="form-group">
            <label class="form-label text-xs font-semibold">最大并发运行数</label>
            <input
              type="number"
              id="new-action-concurrency"
              class="input text-xs w-full"
              value="1"
              min="1"
              max="100"
            />
            <p class="text-xs text-muted-foreground mt-1">控制同一时刻该 Action 允许运行的沙箱容器上限，默认 1</p>
          </div>
        </div>
      `,
      footerHtml: `
        <button class="btn btn-outline" id="dialog-create-action-cancel">取消</button>
        <button class="btn btn-primary" id="dialog-create-action-submit">
          ${icon('plus', 'size-3.5')}
          <span>创建动作</span>
        </button>
      `,
      onMount: (dialogEl, close) => {
        const nameInput = dialogEl.querySelector<HTMLInputElement>('#new-action-name')!;
        const descInput = dialogEl.querySelector<HTMLTextAreaElement>('#new-action-description')!;
        const concurrencyInput = dialogEl.querySelector<HTMLInputElement>('#new-action-concurrency')!;
        const cancelBtn = dialogEl.querySelector<HTMLButtonElement>('#dialog-create-action-cancel')!;
        const submitBtn = dialogEl.querySelector<HTMLButtonElement>('#dialog-create-action-submit')!;

        nameInput.focus();
        cancelBtn.addEventListener('click', () => close());

        submitBtn.addEventListener('click', async () => {
          const name = nameInput.value.trim();
          if (!name) {
            toast.warning('请输入动作名称');
            nameInput.focus();
            return;
          }
          const description = descInput.value.trim();
          const maxConcurrency = parseInt(concurrencyInput.value, 10) || 1;

          submitBtn.disabled = true;
          try {
            const created = await actionsCatAPI.createAction({
              name,
              description,
              max_concurrency: maxConcurrency,
            });
            toast.success(`Action "${created.name || created.id}" 创建成功`);
            close();
            void loadActions();
          } catch (err: unknown) {
            const msg = err instanceof Error ? err.message : String(err);
            toast.error(`创建动作失败: ${msg}`);
            submitBtn.disabled = false;
          }
        });
      },
    });
  }

  // Load All Data
  async function loadAll(): Promise<void> {
    await loadStatus();
    await Promise.all([loadActions(), loadRuns()]);
  }

  // Attach Top Bar Events
  container.querySelector('#actionscat-refresh-btn')?.addEventListener('click', () => {
    void loadAll();
  });
  container.querySelector('#actionscat-create-action-btn')?.addEventListener('click', () => {
    openCreateActionDialog();
  });
  container.querySelector('#actionscat-refresh-runs-btn')?.addEventListener('click', () => {
    void loadRuns();
  });
  container.querySelector('#actionscat-dispatch-btn')?.addEventListener('click', () => {
    openDispatchDialog();
  });
  actionFilterSelect.addEventListener('change', () => {
    selectedActionFilter = actionFilterSelect.value;
    void loadRuns();
  });

  // Initial Load
  void loadAll();

  return () => {
    isDisposed = true;
  };
}
