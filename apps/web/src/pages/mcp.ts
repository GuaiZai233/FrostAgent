import { api, type MCPServerInfo, type MCPToolInfo } from '../api/client';
import { escapeHtml } from '../utils/formatters';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { openDialog } from '../components/dialog';
import { confirmDialog } from '../components/confirm';
import { copyToClipboard } from '../utils/clipboard';

export function mountMCPPage(container: HTMLElement): () => void {
  let isUnmounted = false;
  let loading = false;
  let servers: MCPServerInfo[] = [];
  const expandedServers = new Set<string>();

  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="flex items-center justify-between gap-4 flex-wrap pb-1">
        <div>
          <h1 class="page-title">MCP 服务器</h1>
          <p class="page-description">管理模型上下文协议 (Model Context Protocol) 外部工具提供者与动态工具目录</p>
        </div>
        <div class="flex items-center gap-2 flex-wrap">
          <button class="btn btn-outline" id="mcp-refresh-btn" title="刷新服务器列表">
            <span class="inline-flex" id="mcp-refresh-icon">${icon('refresh')}</span>
            <span>刷新</span>
          </button>
          <button class="btn btn-primary" id="mcp-add-server-btn">
            ${icon('plus')}
            <span>添加服务器</span>
          </button>
        </div>
      </header>

      <!-- Stats Cards -->
      <section class="grid grid-cols-4 gap-3" id="mcp-stats-section">
        <article class="card p-3 flex flex-col gap-1">
          <p class="text-xs text-muted font-medium">配置服务器</p>
          <p class="text-xl font-bold tracking-tight text-foreground" id="stat-mcp-total">-</p>
        </article>
        <article class="card p-3 flex flex-col gap-1">
          <p class="text-xs text-muted font-medium">已连接 / 正常</p>
          <p class="text-xl font-bold tracking-tight text-foreground" id="stat-mcp-connected">-</p>
        </article>
        <article class="card p-3 flex flex-col gap-1">
          <p class="text-xs text-muted font-medium">发现工具数</p>
          <p class="text-xl font-bold tracking-tight text-foreground" id="stat-mcp-tools">-</p>
        </article>
        <article class="card p-3 flex flex-col gap-1">
          <p class="text-xs text-muted font-medium">已启用工具</p>
          <p class="text-xl font-bold tracking-tight text-foreground" id="stat-mcp-enabled-tools">-</p>
        </article>
      </section>

      <!-- Server List -->
      <div id="mcp-servers-container" class="flex flex-col gap-4">
        <div class="card p-8 text-center text-muted">
          <span class="spinner"></span>
          <span class="ml-2">正在获取 MCP 服务器配置...</span>
        </div>
      </div>
    </div>
  `;

  const serversContainer = container.querySelector('#mcp-servers-container') as HTMLElement;
  const refreshBtn = container.querySelector('#mcp-refresh-btn') as HTMLButtonElement;
  const addBtn = container.querySelector('#mcp-add-server-btn') as HTMLButtonElement;

  refreshBtn.addEventListener('click', () => loadServers());
  addBtn.addEventListener('click', () => openServerFormDialog());

  async function loadServers(): Promise<void> {
    if (loading) return;
    loading = true;
    const refreshIcon = container.querySelector('#mcp-refresh-icon');
    refreshIcon?.classList.add('animate-spin');

    try {
      const resp = await api.listMCPServers();
      if (isUnmounted) return;
      servers = resp.servers || [];

      // Default expand all servers on first load if not set
      if (expandedServers.size === 0) {
        for (const s of servers) {
          expandedServers.add(s.id);
        }
      }

      updateStats();
      renderServerList();
    } catch (err: unknown) {
      if (isUnmounted) return;
      const msg = err instanceof Error ? err.message : String(err);
      toast.error(`获取 MCP 服务器列表失败: ${msg}`);
      serversContainer.innerHTML = `
        <div class="card p-8 text-center text-muted">
          <p class="text-destructive font-medium mb-2">获取列表失败</p>
          <p class="text-xs text-muted mb-4">${escapeHtml(msg)}</p>
          <button class="btn btn-outline btn-sm" id="mcp-retry-load">重试</button>
        </div>
      `;
      container.querySelector('#mcp-retry-load')?.addEventListener('click', () => loadServers());
    } finally {
      loading = false;
      refreshIcon?.classList.remove('animate-spin');
    }
  }

  function updateStats(): void {
    const totalEl = container.querySelector('#stat-mcp-total');
    const connEl = container.querySelector('#stat-mcp-connected');
    const toolsEl = container.querySelector('#stat-mcp-tools');
    const enabledToolsEl = container.querySelector('#stat-mcp-enabled-tools');

    const total = servers.length;
    let connected = 0;
    let totalTools = 0;
    let enabledTools = 0;

    for (const s of servers) {
      if (s.status === 'connected') connected++;
      totalTools += s.toolsCount;
      enabledTools += s.enabledToolsCount;
    }

    if (totalEl) totalEl.textContent = String(total);
    if (connEl) connEl.textContent = String(connected);
    if (toolsEl) toolsEl.textContent = String(totalTools);
    if (enabledToolsEl) enabledToolsEl.textContent = String(enabledTools);
  }

  function getStatusBadge(status: string): { label: string; className: string } {
    switch (status) {
      case 'connected':
        return { label: '已连接', className: 'badge-success' };
      case 'starting':
        return { label: '启动中', className: 'badge-warning' };
      case 'failed':
        return { label: '异常', className: 'badge-destructive' };
      case 'stopped':
      default:
        return { label: '已停止', className: 'badge-outline' };
    }
  }

  function renderServerList(): void {
    if (servers.length === 0) {
      serversContainer.innerHTML = `
        <div class="card p-12 text-center text-muted">
          <div class="inline-flex p-3 rounded-full bg-secondary mb-3 text-muted-foreground">
            ${icon('server', 'size-8')}
          </div>
          <h3 class="text-base font-semibold text-foreground mb-1">尚未配置任何 MCP 服务器</h3>
          <p class="text-xs text-muted max-w-md mx-auto mb-4">
            MCP (Model Context Protocol) 允许智能体动态接入外部工具（如文件操作、API调用、代码分析等）。
          </p>
          <button class="btn btn-primary btn-sm" id="mcp-empty-add-btn">
            ${icon('plus')}
            <span>添加第一个 MCP 服务器</span>
          </button>
        </div>
      `;
      container.querySelector('#mcp-empty-add-btn')?.addEventListener('click', () => openServerFormDialog());
      return;
    }

    serversContainer.innerHTML = servers
      .map((srv) => {
        const isExpanded = expandedServers.has(srv.id);
        const statusBadge = getStatusBadge(srv.status);
        const isStdio = srv.transportType === 'stdio';

        return `
          <div class="card overflow-hidden" data-server-id="${escapeHtml(srv.id)}">
            <!-- Server Card Header -->
            <div class="p-4 flex items-center justify-between gap-3 flex-wrap border-b border-border bg-card">
              <div class="flex items-center gap-3">
                <button
                  class="btn btn-ghost btn-icon-sm"
                  data-action="toggle-expand"
                  data-id="${escapeHtml(srv.id)}"
                  title="${isExpanded ? '收起工具列表' : '展开工具列表'}"
                >
                  ${icon(isExpanded ? 'chevron_up' : 'chevron_down', 'size-4')}
                </button>
                <div>
                  <div class="flex items-center gap-2">
                    <h3 class="text-base font-bold text-foreground">${escapeHtml(srv.name)}</h3>
                    <span class="badge font-mono text-xs">${escapeHtml(srv.id)}</span>
                    <span class="badge ${statusBadge.className}">${statusBadge.label}</span>
                    <span class="badge badge-outline text-xs">${escapeHtml(srv.transportType)}</span>
                  </div>
                  <div class="text-xs text-muted font-mono mt-1">
                    ${
                      isStdio
                        ? `命令: ${escapeHtml(srv.command)} ${escapeHtml((srv.args || []).join(' '))}`
                        : `URL: ${escapeHtml(srv.url)}`
                    }
                  </div>
                </div>
              </div>

              <!-- Top Actions -->
              <div class="flex items-center gap-2">
                <button
                  class="btn btn-sm ${srv.enabled ? 'btn-secondary' : 'btn-outline'}"
                  data-action="toggle-server"
                  data-id="${escapeHtml(srv.id)}"
                  data-enabled="${srv.enabled ? 'true' : 'false'}"
                  title="${srv.enabled ? '停用此服务器' : '启用此服务器'}"
                >
                  ${icon(srv.enabled ? 'eye_off' : 'eye', 'size-3.5')}
                  <span>${srv.enabled ? '已启用' : '已停用'}</span>
                </button>

                <button
                  class="btn btn-outline btn-sm"
                  data-action="sync-server"
                  data-id="${escapeHtml(srv.id)}"
                  title="重新握手并刷新工具目录"
                >
                  ${icon('refresh', 'size-3.5')}
                  <span>重新连接</span>
                </button>

                <button
                  class="btn btn-outline btn-sm"
                  data-action="edit-server"
                  data-id="${escapeHtml(srv.id)}"
                  title="编辑服务器配置"
                >
                  ${icon('edit', 'size-3.5')}
                  <span>配置</span>
                </button>

                <button
                  class="btn btn-ghost btn-icon-sm text-destructive"
                  data-action="delete-server"
                  data-id="${escapeHtml(srv.id)}"
                  data-name="${escapeHtml(srv.name)}"
                  title="删除此服务器"
                >
                  ${icon('trash', 'size-4')}
                </button>
              </div>
            </div>

            <!-- Error Banner (if any) -->
            ${
              srv.lastError
                ? `
                <div class="p-3 bg-destructive/10 border-b border-destructive/20 text-xs text-destructive flex items-start gap-2">
                  <span class="inline-flex mt-0.5">${icon('circle_alert', 'size-4')}</span>
                  <div>
                    <strong>连接异常:</strong> ${escapeHtml(srv.lastError)}
                  </div>
                </div>
              `
                : ''
            }

            <!-- Server Tools Body -->
            ${
              isExpanded
                ? `
                <div class="p-4 bg-muted/20">
                  <div class="flex items-center justify-between gap-2 mb-3">
                    <div class="flex items-center gap-2">
                      <span class="text-xs font-semibold text-foreground">提供工具</span>
                      <span class="badge badge-outline text-xs">${srv.tools.length} 个工具 (${srv.enabledToolsCount} 启用)</span>
                    </div>
                    <span class="text-xs text-muted">工具名称格式: <code>mcp__&lt;server_id&gt;__&lt;tool_name&gt;</code></span>
                  </div>

                  ${
                    srv.tools.length === 0
                      ? `
                      <div class="text-xs text-muted text-center py-6 border border-dashed border-border rounded-md bg-card">
                        ${
                          srv.status === 'connected'
                            ? '该服务器未声明任何工具。'
                            : srv.status === 'stopped'
                            ? '服务器已停用，启动连接后将自动拉取工具列表。'
                            : '连接异常或正在连接中，拉取工具失败。'
                        }
                      </div>
                    `
                      : `
                      <div class="table-container rounded-md border border-border bg-card">
                        <table class="table text-xs">
                          <thead>
                            <tr>
                              <th style="width: 5rem;">状态</th>
                              <th style="width: 14rem;">工具名称</th>
                              <th>描述</th>
                              <th style="width: 10rem; text-align: right;">操作</th>
                            </tr>
                          </thead>
                          <tbody>
                            ${srv.tools
                              .map(
                                (tool) => `
                              <tr>
                                <td>
                                  <button
                                    class="btn btn-sm ${tool.enabled ? 'btn-secondary' : 'btn-outline'}"
                                    data-action="toggle-tool"
                                    data-server-id="${escapeHtml(srv.id)}"
                                    data-tool-name="${escapeHtml(tool.name)}"
                                    data-enabled="${tool.enabled ? 'true' : 'false'}"
                                    title="${tool.enabled ? '停用此工具' : '启用此工具'}"
                                    style="height: 1.75rem; padding: 0 0.5rem; font-size: 0.75rem;"
                                  >
                                    ${tool.enabled ? '已启用' : '已停用'}
                                  </button>
                                </td>
                                <td>
                                  <div class="flex flex-col gap-0.5">
                                    <span class="font-bold font-mono text-foreground">${escapeHtml(tool.name)}</span>
                                    <div class="flex items-center gap-1 text-muted font-mono" style="font-size: 0.7rem;">
                                      <span>${escapeHtml(tool.fullName)}</span>
                                      <button
                                        class="btn btn-ghost btn-icon-sm"
                                        style="width: 1.25rem; height: 1.25rem;"
                                        data-action="copy-name"
                                        data-name="${escapeHtml(tool.fullName)}"
                                        title="复制完整命名空间名称"
                                      >
                                        ${icon('copy', 'size-3')}
                                      </button>
                                    </div>
                                  </div>
                                </td>
                                <td class="text-muted-foreground leading-relaxed">
                                  ${escapeHtml(tool.description || '无描述')}
                                </td>
                                <td style="text-align: right;">
                                  <button
                                    class="btn btn-outline btn-sm"
                                    data-action="view-schema"
                                    data-server-id="${escapeHtml(srv.id)}"
                                    data-tool-name="${escapeHtml(tool.name)}"
                                    title="查看参数规范 (JSON Schema)"
                                    style="height: 1.75rem; padding: 0 0.625rem; font-size: 0.75rem;"
                                  >
                                    ${icon('file_code', 'size-3')}
                                    <span>参数 Schema</span>
                                  </button>
                                </td>
                              </tr>
                            `,
                              )
                              .join('')}
                          </tbody>
                        </table>
                      </div>
                    `
                  }
                </div>
              `
                : ''
            }
          </div>
        `;
      })
      .join('');

    attachServerEvents();
  }

  function attachServerEvents(): void {
    // Toggle expand
    serversContainer.querySelectorAll('[data-action="toggle-expand"]').forEach((btn) => {
      btn.addEventListener('click', (e) => {
        const target = e.currentTarget as HTMLElement;
        const id = target.dataset.id;
        if (!id) return;
        if (expandedServers.has(id)) {
          expandedServers.delete(id);
        } else {
          expandedServers.add(id);
        }
        renderServerList();
      });
    });

    // Toggle server enabled
    serversContainer.querySelectorAll('[data-action="toggle-server"]').forEach((btn) => {
      btn.addEventListener('click', async (e) => {
        const target = e.currentTarget as HTMLElement;
        const id = target.dataset.id;
        const currentlyEnabled = target.dataset.enabled === 'true';
        if (!id) return;

        target.setAttribute('disabled', 'true');
        try {
          const resp = await api.toggleMCPServer(id, !currentlyEnabled);
          if (!resp.success) {
            toast.error(`切换服务器状态失败: ${resp.error}`);
            return;
          }
          toast.success(`MCP 服务器 "${id}" 已${!currentlyEnabled ? '启用' : '停用'}`);
          await loadServers();
        } catch (err: unknown) {
          const msg = err instanceof Error ? err.message : String(err);
          toast.error(`切换服务器状态失败: ${msg}`);
        } finally {
          target.removeAttribute('disabled');
        }
      });
    });

    // Sync server
    serversContainer.querySelectorAll('[data-action="sync-server"]').forEach((btn) => {
      btn.addEventListener('click', async (e) => {
        const target = e.currentTarget as HTMLElement;
        const id = target.dataset.id;
        if (!id) return;

        target.setAttribute('disabled', 'true');
        const originalText = target.innerHTML;
        target.innerHTML = `<span class="spinner inline-block" style="width:0.875rem;height:0.875rem"></span> <span>连接中...</span>`;

        try {
          const resp = await api.syncMCPServer(id);
          if (!resp.success) {
            toast.error(`重新连接失败: ${resp.error}`);
          } else {
            toast.success(`MCP 服务器 "${id}" 目录同步完成`);
          }
          await loadServers();
        } catch (err: unknown) {
          const msg = err instanceof Error ? err.message : String(err);
          toast.error(`重新连接失败: ${msg}`);
        } finally {
          target.removeAttribute('disabled');
          target.innerHTML = originalText;
        }
      });
    });

    // Edit server
    serversContainer.querySelectorAll('[data-action="edit-server"]').forEach((btn) => {
      btn.addEventListener('click', (e) => {
        const target = e.currentTarget as HTMLElement;
        const id = target.dataset.id;
        if (!id) return;
        const srv = servers.find((s) => s.id === id);
        if (srv) {
          openServerFormDialog(srv);
        }
      });
    });

    // Delete server
    serversContainer.querySelectorAll('[data-action="delete-server"]').forEach((btn) => {
      btn.addEventListener('click', async (e) => {
        const target = e.currentTarget as HTMLElement;
        const id = target.dataset.id;
        const name = target.dataset.name;
        if (!id) return;

        const confirmed = await confirmDialog({
          title: '删除 MCP 服务器',
          message: `确定要删除服务器 "${name || id}" (${id}) 吗？此操作将停止其子进程/连接并移除相关工具。`,
          confirmLabel: '删除',
          cancelLabel: '取消',
          destructive: true,
        });
        if (!confirmed) return;

        try {
          const resp = await api.deleteMCPServer(id);
          if (!resp.success) {
            toast.error(`删除失败: ${resp.error}`);
            return;
          }
          toast.success(`MCP 服务器 "${id}" 已删除`);
          await loadServers();
        } catch (err: unknown) {
          const msg = err instanceof Error ? err.message : String(err);
          toast.error(`删除失败: ${msg}`);
        }
      });
    });

    // Toggle tool enabled
    serversContainer.querySelectorAll('[data-action="toggle-tool"]').forEach((btn) => {
      btn.addEventListener('click', async (e) => {
        const target = e.currentTarget as HTMLElement;
        const serverId = target.dataset.serverId;
        const toolName = target.dataset.toolName;
        const currentlyEnabled = target.dataset.enabled === 'true';
        if (!serverId || !toolName) return;

        target.setAttribute('disabled', 'true');
        try {
          const resp = await api.toggleMCPTool(serverId, toolName, !currentlyEnabled);
          if (!resp.success) {
            toast.error(`切换工具开关失败: ${resp.error}`);
            return;
          }
          toast.success(`工具 "${toolName}" 已${!currentlyEnabled ? '启用' : '停用'}`);
          await loadServers();
        } catch (err: unknown) {
          const msg = err instanceof Error ? err.message : String(err);
          toast.error(`切换工具开关失败: ${msg}`);
        } finally {
          target.removeAttribute('disabled');
        }
      });
    });

    // Copy tool name
    serversContainer.querySelectorAll('[data-action="copy-name"]').forEach((btn) => {
      btn.addEventListener('click', async (e) => {
        const target = e.currentTarget as HTMLElement;
        const name = target.dataset.name;
        if (!name) return;
        await copyToClipboard(name);
        toast.success(`已复制: ${name}`);
      });
    });

    // View schema
    serversContainer.querySelectorAll('[data-action="view-schema"]').forEach((btn) => {
      btn.addEventListener('click', (e) => {
        const target = e.currentTarget as HTMLElement;
        const serverId = target.dataset.serverId;
        const toolName = target.dataset.toolName;
        if (!serverId || !toolName) return;

        const srv = servers.find((s) => s.id === serverId);
        const tool = srv?.tools.find((t) => t.name === toolName);
        if (tool) {
          openToolSchemaDialog(tool, serverId);
        }
      });
    });
  }

  function openToolSchemaDialog(tool: MCPToolInfo, serverId: string): void {
    let prettyJSON = '{}';
    try {
      if (tool.parametersJson) {
        const parsed = JSON.parse(tool.parametersJson);
        prettyJSON = JSON.stringify(parsed, null, 2);
      }
    } catch {
      prettyJSON = tool.parametersJson || '{}';
    }

    openDialog({
      title: `工具参数: ${tool.name}`,
      description: `完整名称: ${tool.fullName} (由 ${serverId} 提供)`,
      maxWidth: '38rem',
      bodyHtml: `
        <div class="flex flex-col gap-3">
          <div>
            <label class="text-xs font-semibold text-foreground mb-1 block">功能描述</label>
            <p class="text-xs text-muted leading-relaxed">${escapeHtml(tool.description || '无描述')}</p>
          </div>
          <div>
            <div class="flex items-center justify-between mb-1">
              <label class="text-xs font-semibold text-foreground">OpenAI 兼容参数 Schema (JSON)</label>
              <button class="btn btn-ghost btn-icon-sm" id="copy-schema-btn" title="复制 Schema">
                ${icon('copy', 'size-3.5')}
              </button>
            </div>
            <pre class="bg-muted/50 p-3 rounded-md text-xs font-mono overflow-auto max-h-72 border border-border"><code>${escapeHtml(prettyJSON)}</code></pre>
          </div>
        </div>
      `,
      footerHtml: `
        <button class="btn btn-primary btn-sm dialog-close-btn">关闭</button>
      `,
      onMount: (dialogEl) => {
        dialogEl.querySelector('#copy-schema-btn')?.addEventListener('click', async () => {
          await copyToClipboard(prettyJSON);
          toast.success('Schema 已复制到剪贴板');
        });
      },
    });
  }

  function openServerFormDialog(existing?: MCPServerInfo): void {
    const isEdit = Boolean(existing);
    const transportType = existing?.transportType || 'stdio';

    // Format env map into KEY=VALUE lines
    const envLines = existing?.env
      ? Object.entries(existing.env)
          .map(([k, v]) => `${k}=${v}`)
          .join('\n')
      : '';

    // Format headers map into Header: value lines
    const headerLines = existing?.headers
      ? Object.entries(existing.headers)
          .map(([k, v]) => `${k}: ${v}`)
          .join('\n')
      : '';

    const argsString = (existing?.args || []).join(' ');

    openDialog({
      title: isEdit ? `编辑 MCP 服务器: ${existing?.name}` : '添加 MCP 服务器',
      description: isEdit
        ? '修改传输方式或命令参数（保存后将自动应用并重新连接）'
        : '配置新的外部 MCP 服务（支持 stdio 进程管道或 streamable-http/SSE）',
      maxWidth: '36rem',
      bodyHtml: `
        <form id="mcp-server-form" class="flex flex-col gap-4 text-xs">
          <div class="grid grid-cols-2 gap-3">
            <div>
              <label class="font-semibold text-foreground mb-1 block">服务器标识 (ID) *</label>
              <input
                type="text"
                id="form-id"
                class="input w-full font-mono"
                placeholder="例如: weather_svc"
                value="${escapeHtml(existing?.id || '')}"
                ${isEdit ? 'readonly disabled style="opacity: 0.7;"' : 'required'}
              />
              <span class="text-muted text-xs block mt-0.5">英文字母、数字与下划线，不能含 "__"</span>
            </div>
            <div>
              <label class="font-semibold text-foreground mb-1 block">显示名称 *</label>
              <input
                type="text"
                id="form-name"
                class="input w-full"
                placeholder="例如: 天气查询服务"
                value="${escapeHtml(existing?.name || '')}"
                required
              />
            </div>
          </div>

          <div class="flex items-center gap-2">
            <input
              type="checkbox"
              id="form-enabled"
              ${existing ? (existing.enabled ? 'checked' : '') : 'checked'}
            />
            <label for="form-enabled" class="font-medium text-foreground cursor-pointer">启用此 MCP 服务器</label>
          </div>

          <div>
            <label class="font-semibold text-foreground mb-1 block">通信方式 (Transport) *</label>
            <div class="flex items-center gap-4 mt-1">
              <label class="flex items-center gap-1.5 cursor-pointer">
                <input type="radio" name="form-transport" value="stdio" ${transportType === 'stdio' ? 'checked' : ''} />
                <span>Stdio (本地子进程 stdin/stdout)</span>
              </label>
              <label class="flex items-center gap-1.5 cursor-pointer">
                <input type="radio" name="form-transport" value="http" ${transportType === 'http' ? 'checked' : ''} />
                <span>HTTP (远程 SSE / Streamable HTTP)</span>
              </label>
            </div>
          </div>

          <!-- Stdio Fields -->
          <div id="stdio-fields" class="flex flex-col gap-3 p-3 rounded-md border border-border bg-muted/10 ${transportType === 'stdio' ? '' : 'hidden'}">
            <div>
              <label class="font-semibold text-foreground mb-1 block">执行命令 (Command) *</label>
              <input
                type="text"
                id="form-command"
                class="input w-full font-mono"
                placeholder="例如: npx 或 python 或 /path/to/server"
                value="${escapeHtml(existing?.command || '')}"
              />
            </div>
            <div>
              <label class="font-semibold text-foreground mb-1 block">命令行参数 (Args)</label>
              <input
                type="text"
                id="form-args"
                class="input w-full font-mono"
                placeholder="以空格分隔，例如: -y @modelcontextprotocol/server-everything"
                value="${escapeHtml(argsString)}"
              />
            </div>
            <div>
              <label class="font-semibold text-foreground mb-1 block">工作目录 (Working Directory, 可选)</label>
              <input
                type="text"
                id="form-working-dir"
                class="input w-full font-mono"
                placeholder="留空则使用当前工作目录"
                value="${escapeHtml(existing?.workingDir || '')}"
              />
            </div>
            <div>
              <label class="font-semibold text-foreground mb-1 block">环境变量 (Environment Variables, 每行一个 KEY=VALUE)</label>
              <textarea
                id="form-env"
                class="textarea w-full font-mono"
                rows="3"
                placeholder="API_KEY=your_key&#10;DEBUG=1"
              >${escapeHtml(envLines)}</textarea>
            </div>
          </div>

          <!-- HTTP Fields -->
          <div id="http-fields" class="flex flex-col gap-3 p-3 rounded-md border border-border bg-muted/10 ${transportType === 'http' ? '' : 'hidden'}">
            <div>
              <label class="font-semibold text-foreground mb-1 block">服务器 URL *</label>
              <input
                type="url"
                id="form-url"
                class="input w-full font-mono"
                placeholder="http://localhost:8000/sse"
                value="${escapeHtml(existing?.url || '')}"
              />
            </div>
            <div>
              <label class="font-semibold text-foreground mb-1 block">请求标头 (Headers, 每行一个 Header: value)</label>
              <textarea
                id="form-headers"
                class="textarea w-full font-mono"
                rows="3"
                placeholder="Authorization: Bearer token_here"
              >${escapeHtml(headerLines)}</textarea>
            </div>
          </div>
        </form>
      `,
      footerHtml: `
        <button class="btn btn-outline btn-sm dialog-close-btn">取消</button>
        <button class="btn btn-primary btn-sm" id="form-submit-btn">保存并应用</button>
      `,
      onMount: (dialogEl, close) => {
        const stdioFields = dialogEl.querySelector('#stdio-fields') as HTMLElement;
        const httpFields = dialogEl.querySelector('#http-fields') as HTMLElement;
        const submitBtn = dialogEl.querySelector('#form-submit-btn') as HTMLButtonElement;

        dialogEl.querySelectorAll('input[name="form-transport"]').forEach((radio) => {
          radio.addEventListener('change', (e) => {
            const val = (e.target as HTMLInputElement).value;
            if (val === 'stdio') {
              stdioFields.classList.remove('hidden');
              httpFields.classList.add('hidden');
            } else {
              stdioFields.classList.add('hidden');
              httpFields.classList.remove('hidden');
            }
          });
        });

        submitBtn.addEventListener('click', async () => {
          const id = (dialogEl.querySelector('#form-id') as HTMLInputElement).value.trim();
          const name = (dialogEl.querySelector('#form-name') as HTMLInputElement).value.trim();
          const enabled = (dialogEl.querySelector('#form-enabled') as HTMLInputElement).checked;
          const transport = (
            dialogEl.querySelector('input[name="form-transport"]:checked') as HTMLInputElement
          ).value;

          if (!id) {
            toast.error('请输入服务器标识 (ID)');
            return;
          }
          if (id.includes('__')) {
            toast.error('服务器标识不能包含双下划线 "__"');
            return;
          }
          if (!name) {
            toast.error('请输入显示名称');
            return;
          }

          let command = '';
          let args: string[] = [];
          let workingDir = '';
          const envMap: Record<string, string> = {};
          let url = '';
          const headerMap: Record<string, string> = {};

          if (transport === 'stdio') {
            command = (dialogEl.querySelector('#form-command') as HTMLInputElement).value.trim();
            if (!command) {
              toast.error('请输入执行命令');
              return;
            }
            const argsRaw = (dialogEl.querySelector('#form-args') as HTMLInputElement).value.trim();
            if (argsRaw) {
              // Parse space-separated args, preserving quotes
              args = argsRaw.split(/\s+/).filter(Boolean);
            }
            workingDir = (dialogEl.querySelector('#form-working-dir') as HTMLInputElement).value.trim();
            const envRaw = (dialogEl.querySelector('#form-env') as HTMLTextAreaElement).value.trim();
            if (envRaw) {
              for (const line of envRaw.split('\n')) {
                const trimmed = line.trim();
                if (!trimmed || trimmed.startsWith('#')) continue;
                const eqIdx = trimmed.indexOf('=');
                if (eqIdx > 0) {
                  envMap[trimmed.slice(0, eqIdx).trim()] = trimmed.slice(eqIdx + 1).trim();
                }
              }
            }
          } else {
            url = (dialogEl.querySelector('#form-url') as HTMLInputElement).value.trim();
            if (!url) {
              toast.error('请输入服务器 URL');
              return;
            }
            const headersRaw = (dialogEl.querySelector('#form-headers') as HTMLTextAreaElement).value.trim();
            if (headersRaw) {
              for (const line of headersRaw.split('\n')) {
                const trimmed = line.trim();
                if (!trimmed || trimmed.startsWith('#')) continue;
                const colonIdx = trimmed.indexOf(':');
                if (colonIdx > 0) {
                  headerMap[trimmed.slice(0, colonIdx).trim()] = trimmed.slice(colonIdx + 1).trim();
                }
              }
            }
          }

          submitBtn.setAttribute('disabled', 'true');
          submitBtn.innerHTML = `<span class="spinner inline-block" style="width:0.875rem;height:0.875rem"></span> 正在保存...`;

          try {
            if (isEdit) {
              const resp = await api.updateMCPServer({
                id,
                name,
                enabled,
                transportType: transport,
                command,
                args,
                env: envMap,
                workingDir,
                url,
                headers: headerMap,
              });
              if (!resp.success) {
                toast.error(`更新服务器失败: ${resp.error}`);
                return;
              }
              toast.success(`MCP 服务器 "${name}" 配置已保存`);
            } else {
              const resp = await api.addMCPServer({
                id,
                name,
                enabled,
                transportType: transport,
                command,
                args,
                env: envMap,
                workingDir,
                url,
                headers: headerMap,
              });
              if (!resp.success) {
                toast.error(`添加服务器失败: ${resp.error}`);
                return;
              }
              toast.success(`MCP 服务器 "${name}" 已成功添加并启动连接`);
              expandedServers.add(id);
            }

            close();
            await loadServers();
          } catch (err: unknown) {
            const msg = err instanceof Error ? err.message : String(err);
            toast.error(`操作失败: ${msg}`);
          } finally {
            submitBtn.removeAttribute('disabled');
            submitBtn.textContent = '保存并应用';
          }
        });
      },
    });
  }

  // Initial load
  loadServers();

  return () => {
    isUnmounted = true;
  };
}
