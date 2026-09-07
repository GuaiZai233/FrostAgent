import { api, type MCPServerInfo, type MCPToolInfo } from '../api/client';
import { escapeHtml } from '../utils/formatters';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { openDialog } from '../components/dialog';
import { confirmDialog } from '../components/confirm';
import { copyToClipboard } from '../utils/clipboard';

// Helper utilities for bi-directional JSON & Form synchronization
function cleanJSON(raw: string): string {
  return raw
    .replace(/\/\/.*$/gm, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/,\s*([}\]])/g, '$1')
    .trim();
}

function parseArgsText(text: string): string[] {
  const trimmed = text.trim();
  if (!trimmed) return [];
  const matches = trimmed.match(/[^\s"']+|"([^"]*)"|'([^']*)'/g) || [];
  return matches.map((m) => {
    if ((m.startsWith('"') && m.endsWith('"')) || (m.startsWith("'") && m.endsWith("'"))) {
      return m.slice(1, -1);
    }
    return m;
  });
}

function formatArgsArray(args?: string[]): string {
  if (!args || args.length === 0) return '';
  return args
    .map((a) => (a.includes(' ') ? `"${a}"` : a))
    .join(' ');
}

function parseEnvText(text: string): Record<string, string> {
  const envMap: Record<string, string> = {};
  for (const line of text.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;
    const eqIdx = trimmed.indexOf('=');
    if (eqIdx > 0) {
      envMap[trimmed.slice(0, eqIdx).trim()] = trimmed.slice(eqIdx + 1).trim();
    }
  }
  return envMap;
}

function formatEnvMap(map?: Record<string, string>): string {
  if (!map) return '';
  return Object.entries(map)
    .map(([k, v]) => `${k}=${v}`)
    .join('\n');
}

function parseHeadersText(text: string): Record<string, string> {
  const headerMap: Record<string, string> = {};
  for (const line of text.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;
    const colonIdx = trimmed.indexOf(':');
    if (colonIdx > 0) {
      headerMap[trimmed.slice(0, colonIdx).trim()] = trimmed.slice(colonIdx + 1).trim();
    }
  }
  return headerMap;
}

function formatHeadersMap(map?: Record<string, string>): string {
  if (!map) return '';
  return Object.entries(map)
    .map(([k, v]) => `${k}: ${v}`)
    .join('\n');
}

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
          <p class="page-description">连接外部 MCP Server，为 Agent 动态扩展工具能力</p>
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

      <!-- Compact Summary Bar (visible only when servers exist) -->
      <div id="mcp-summary-bar" class="mcp-summary-bar" style="display: none;"></div>

      <!-- Server List or Clean Empty State -->
      <div id="mcp-servers-container" class="flex flex-col gap-3">
        <div class="card p-8 text-center text-muted">
          <span class="spinner"></span>
          <span class="ml-2">正在获取 MCP 服务器配置...</span>
        </div>
      </div>
    </div>

    <style>
      .mcp-summary-bar {
        display: flex;
        align-items: center;
        gap: 0.75rem;
        font-size: 0.8125rem;
        color: var(--muted-foreground);
        padding: 0.125rem 0.25rem;
        flex-wrap: wrap;
      }
      .mcp-summary-divider {
        color: var(--border);
        user-select: none;
      }
      .mcp-status-dot {
        width: 0.5rem;
        height: 0.5rem;
        border-radius: 50%;
        display: inline-block;
        flex-shrink: 0;
      }
      .mcp-status-dot.connected {
        background-color: var(--success);
        box-shadow: 0 0 0 2px var(--success-bg);
      }
      .mcp-status-dot.starting {
        background-color: var(--warning);
        box-shadow: 0 0 0 2px var(--warning-bg);
      }
      .mcp-status-dot.failed {
        background-color: var(--destructive);
        box-shadow: 0 0 0 2px rgba(239, 68, 68, 0.2);
      }
      .mcp-status-dot.stopped {
        background-color: var(--muted-foreground);
        opacity: 0.5;
      }

      /* Empty State - Centered & Immune to Flex Stretching */
      .mcp-empty-card {
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        background-color: var(--card);
      }
      .mcp-empty-state {
        display: flex;
        flex-direction: column;
        align-items: center;
        justify-content: center;
        text-align: center;
        padding: 3.5rem 1.5rem;
      }
      .mcp-empty-icon {
        display: flex;
        align-items: center;
        justify-content: center;
        width: 3.25rem;
        height: 3.25rem;
        border-radius: var(--radius-full);
        background-color: var(--secondary);
        color: var(--muted-foreground);
        margin-bottom: 1rem;
        flex-shrink: 0;
      }
      .mcp-empty-icon svg {
        width: 1.75rem;
        height: 1.75rem;
      }
      .mcp-empty-title {
        font-size: 1rem;
        font-weight: 600;
        color: var(--foreground);
        margin-bottom: 0.375rem;
      }
      .mcp-empty-desc {
        font-size: 0.8125rem;
        color: var(--muted-foreground);
        margin-bottom: 1.25rem;
        max-width: 24rem;
        line-height: 1.5;
      }
      #mcp-empty-add-btn {
        display: inline-flex;
        width: auto;
      }

      /* Server Card Styles */
      .mcp-server-card {
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        background-color: var(--card);
        overflow: hidden;
        transition: border-color 0.15s ease, box-shadow 0.15s ease;
      }
      .mcp-server-card:hover {
        border-color: color-mix(in srgb, var(--primary) 25%, var(--border));
      }
      .mcp-card-body {
        padding: 1.125rem 1.25rem 0.875rem 1.25rem;
        display: flex;
        flex-direction: column;
      }
      .mcp-card-header {
        display: flex;
        align-items: flex-start;
        justify-content: space-between;
        gap: 1rem;
      }
      .mcp-server-title-group {
        display: flex;
        flex-direction: column;
        gap: 0.25rem;
        min-width: 0;
      }
      .mcp-server-name {
        font-size: 1.0625rem;
        font-weight: 600;
        color: var(--foreground);
        line-height: 1.3;
      }
      .mcp-server-subtitle {
        font-size: 0.75rem;
        font-family: var(--font-mono);
        color: var(--muted-foreground);
        word-break: break-all;
      }
      .mcp-card-status-switch {
        display: flex;
        align-items: center;
        gap: 0.875rem;
        flex-shrink: 0;
      }
      .mcp-card-tools-summary {
        font-size: 0.8125rem;
        color: var(--muted-foreground);
        font-weight: 500;
        margin-top: 0.625rem;
        margin-bottom: 0.75rem;
      }
      .mcp-card-footer {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 0.75rem;
        flex-wrap: wrap;
        padding-top: 0.625rem;
        border-top: 1px solid var(--border);
      }
      .mcp-error-banner {
        padding: 0.5rem 0.75rem;
        margin-bottom: 0.75rem;
        border-radius: var(--radius-sm);
        background-color: rgba(239, 68, 68, 0.1);
        border: 1px solid rgba(239, 68, 68, 0.2);
        color: var(--destructive);
        font-size: 0.75rem;
        display: flex;
        align-items: flex-start;
        gap: 0.5rem;
      }
      .mcp-tools-drawer {
        border-top: 1px solid var(--border);
        background-color: var(--secondary);
        padding: 0.75rem 1.25rem 1rem 1.25rem;
      }
      .mcp-tools-empty {
        font-size: 0.75rem;
        color: var(--muted-foreground);
        text-align: center;
        padding: 1.5rem 1rem;
        border: 1px dashed var(--border);
        border-radius: var(--radius-sm);
        background-color: var(--card);
      }

      /* Master Switch Toggle */
      .mcp-switch {
        position: relative;
        display: inline-flex;
        align-items: center;
        width: 2.25rem;
        height: 1.25rem;
        cursor: pointer;
        flex-shrink: 0;
      }
      .mcp-switch-input {
        opacity: 0;
        width: 0;
        height: 0;
        position: absolute;
      }
      .mcp-switch-slider {
        position: absolute;
        inset: 0;
        background-color: var(--secondary);
        border: 1px solid var(--border);
        border-radius: var(--radius-full);
        transition: all 0.2s ease;
      }
      .mcp-switch-slider::before {
        position: absolute;
        content: "";
        height: 0.875rem;
        width: 0.875rem;
        left: 0.125rem;
        top: 0.125rem;
        background-color: var(--muted-foreground);
        border-radius: 50%;
        transition: all 0.2s ease;
      }
      .mcp-switch-input:checked + .mcp-switch-slider {
        background-color: var(--primary);
        border-color: var(--primary);
      }
      .mcp-switch-input:checked + .mcp-switch-slider::before {
        transform: translateX(1rem);
        background-color: var(--primary-foreground);
      }
      .mcp-switch-input:disabled + .mcp-switch-slider {
        opacity: 0.5;
        cursor: not-allowed;
      }
    </style>
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

      updateSummary();
      renderServerList();
    } catch (err: unknown) {
      if (isUnmounted) return;
      const msg = err instanceof Error ? err.message : String(err);
      toast.error(`获取 MCP 服务器列表失败: ${msg}`);
      const isAuthError =
        msg.toLowerCase().includes('token') ||
        msg.toLowerCase().includes('unauthenticated') ||
        msg.toLowerCase().includes('permission') ||
        msg.toLowerCase().includes('restricted');

      const summaryBar = container.querySelector('#mcp-summary-bar') as HTMLElement | null;
      if (summaryBar) summaryBar.style.display = 'none';

      serversContainer.innerHTML = `
        <div class="card p-8 text-center text-muted">
          <p class="text-destructive font-medium mb-2">获取列表失败</p>
          <p class="text-xs text-muted mb-4">${escapeHtml(msg)}</p>
          <div class="flex items-center justify-center gap-2">
            <button class="btn btn-outline btn-sm" id="mcp-retry-load">重试</button>
            ${
              isAuthError
                ? `<a href="#/settings/frontend" class="btn btn-primary btn-sm" style="text-decoration: none;">前往设置配置 Token</a>`
                : ''
            }
          </div>
        </div>
      `;
      container.querySelector('#mcp-retry-load')?.addEventListener('click', () => loadServers());
    } finally {
      loading = false;
      refreshIcon?.classList.remove('animate-spin');
    }
  }

  function updateSummary(): void {
    const summaryBar = container.querySelector('#mcp-summary-bar') as HTMLElement | null;
    if (!summaryBar) return;

    if (servers.length === 0) {
      summaryBar.style.display = 'none';
      return;
    }

    summaryBar.style.display = 'flex';
    let connected = 0;
    let totalTools = 0;
    let enabledTools = 0;

    for (const s of servers) {
      if (s.status === 'connected') connected++;
      totalTools += s.toolsCount;
      enabledTools += s.enabledToolsCount;
    }

    summaryBar.innerHTML = `
      <span class="inline-flex items-center gap-1.5 text-foreground font-medium">
        <span class="mcp-status-dot ${connected > 0 ? 'connected' : 'stopped'}"></span>
        <span><strong>${connected}</strong> / ${servers.length} 已连接</span>
      </span>
      <span class="mcp-summary-divider">·</span>
      <span><strong class="text-foreground">${totalTools}</strong> 个工具</span>
      <span class="mcp-summary-divider">·</span>
      <span><strong class="text-foreground">${enabledTools}</strong> 已启用</span>
    `;
  }

  function getStatusInfo(status: string): { label: string; dotClass: string } {
    switch (status) {
      case 'connected':
        return { label: '已连接', dotClass: 'connected' };
      case 'starting':
        return { label: '启动中', dotClass: 'starting' };
      case 'failed':
        return { label: '异常', dotClass: 'failed' };
      case 'stopped':
      default:
        return { label: '已停止', dotClass: 'stopped' };
    }
  }

  function renderServerList(): void {
    if (servers.length === 0) {
      serversContainer.innerHTML = `
        <div class="card mcp-empty-card">
          <div class="mcp-empty-state">
            <div class="mcp-empty-icon">
              ${icon('server')}
            </div>
            <h3 class="mcp-empty-title">还没有 MCP 服务器</h3>
            <p class="mcp-empty-desc">添加一个 MCP Server，让 FrostAgent 使用外部工具</p>
            <button class="btn btn-primary btn-sm" id="mcp-empty-add-btn">
              ${icon('plus')}
              <span>添加 MCP 服务器</span>
            </button>
          </div>
        </div>
      `;
      container.querySelector('#mcp-empty-add-btn')?.addEventListener('click', () => openServerFormDialog());
      return;
    }

    serversContainer.innerHTML = servers
      .map((srv) => {
        const isExpanded = expandedServers.has(srv.id);
        const statusInfo = getStatusInfo(srv.status);
        const isStdio = srv.transportType === 'stdio';
        const subtitle = isStdio
          ? `stdio · ${escapeHtml(srv.command)} ${escapeHtml((srv.args || []).join(' '))}`.trim()
          : `${escapeHtml(srv.transportType)} · ${escapeHtml(srv.url)}`;

        return `
          <div class="card mcp-server-card" data-server-id="${escapeHtml(srv.id)}">
            <div class="mcp-card-body">
              <!-- Server Card Header -->
              <div class="mcp-card-header">
                <div class="mcp-server-title-group">
                  <div class="flex items-center gap-2">
                    <h3 class="mcp-server-name">${escapeHtml(srv.name)}</h3>
                    <span class="badge font-mono text-[11px] text-muted-foreground">${escapeHtml(srv.id)}</span>
                  </div>
                  <div class="mcp-server-subtitle">
                    ${subtitle}
                  </div>
                </div>

                <!-- Status & Master Switch -->
                <div class="mcp-card-status-switch">
                  <span class="inline-flex items-center gap-1.5 text-xs font-medium">
                    <span class="mcp-status-dot ${statusInfo.dotClass}"></span>
                    <span class="text-foreground">${statusInfo.label}</span>
                  </span>
                  <label class="mcp-switch" title="${srv.enabled ? '已启用 (点击停用)' : '已停用 (点击启用)'}">
                    <input
                      type="checkbox"
                      class="mcp-switch-input"
                      data-action="toggle-server"
                      data-id="${escapeHtml(srv.id)}"
                      ${srv.enabled ? 'checked' : ''}
                    />
                    <span class="mcp-switch-slider"></span>
                  </label>
                </div>
              </div>

              <!-- Tools Count Summary -->
              <div class="mcp-card-tools-summary">
                ${srv.toolsCount} tools · ${srv.enabledToolsCount} enabled
              </div>

              <!-- Error Banner (if any) -->
              ${
                srv.lastError
                  ? `
                  <div class="mcp-error-banner">
                    <span class="inline-flex mt-0.5">${icon('circle_alert', 'size-3.5')}</span>
                    <div>
                      <strong>连接异常:</strong> ${escapeHtml(srv.lastError)}
                    </div>
                  </div>
                `
                  : ''
              }

              <!-- Card Action Footer -->
              <div class="mcp-card-footer">
                <button
                  class="btn btn-ghost btn-sm text-muted-foreground hover:text-foreground flex items-center gap-1"
                  data-action="toggle-expand"
                  data-id="${escapeHtml(srv.id)}"
                >
                  <span>${isExpanded ? '收起工具' : '工具列表'}</span>
                  ${icon(isExpanded ? 'chevron_up' : 'chevron_down', 'size-3.5')}
                </button>

                <div class="flex items-center gap-1.5">
                  <button
                    class="btn btn-outline btn-sm"
                    data-action="sync-server"
                    data-id="${escapeHtml(srv.id)}"
                    title="重新握手并刷新工具目录"
                  >
                    ${icon('refresh', 'size-3')}
                    <span>重新连接</span>
                  </button>

                  <button
                    class="btn btn-outline btn-sm"
                    data-action="edit-server"
                    data-id="${escapeHtml(srv.id)}"
                    title="编辑服务器配置"
                  >
                    ${icon('edit', 'size-3')}
                    <span>配置</span>
                  </button>

                  <button
                    class="btn btn-ghost btn-icon-sm text-destructive"
                    data-action="delete-server"
                    data-id="${escapeHtml(srv.id)}"
                    data-name="${escapeHtml(srv.name)}"
                    title="删除此服务器"
                  >
                    ${icon('trash', 'size-3.5')}
                  </button>
                </div>
              </div>
            </div>

            <!-- Expandable Tools Drawer -->
            ${
              isExpanded
                ? `
                <div class="mcp-tools-drawer">
                  ${
                    srv.tools.length === 0
                      ? `
                      <div class="mcp-tools-empty">
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
                              <th style="width: 8.5rem; text-align: right;">操作</th>
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
                                    style="height: 1.625rem; padding: 0 0.5rem; font-size: 0.75rem;"
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
                                    style="height: 1.625rem; padding: 0 0.5rem; font-size: 0.75rem;"
                                  >
                                    ${icon('file_code', 'size-3')}
                                    <span>Schema</span>
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
    // Toggle expand drawer
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

    // Toggle master server switch
    serversContainer.querySelectorAll('input[data-action="toggle-server"]').forEach((inputEl) => {
      inputEl.addEventListener('change', async (e) => {
        const target = e.currentTarget as HTMLInputElement;
        const id = target.dataset.id;
        const targetEnabled = target.checked;
        if (!id) return;

        target.disabled = true;
        try {
          const resp = await api.toggleMCPServer(id, targetEnabled);
          if (!resp.success) {
            toast.error(`切换服务器状态失败: ${resp.error}`);
            target.checked = !targetEnabled;
            return;
          }
          toast.success(`MCP 服务器 "${id}" 已${targetEnabled ? '启用' : '停用'}`);
          await loadServers();
        } catch (err: unknown) {
          const msg = err instanceof Error ? err.message : String(err);
          toast.error(`切换服务器状态失败: ${msg}`);
          target.checked = !targetEnabled;
        } finally {
          target.disabled = false;
        }
      });
    });

    // Reconnect / sync server
    serversContainer.querySelectorAll('[data-action="sync-server"]').forEach((btn) => {
      btn.addEventListener('click', async (e) => {
        const target = e.currentTarget as HTMLElement;
        const id = target.dataset.id;
        if (!id) return;

        target.setAttribute('disabled', 'true');
        const originalHtml = target.innerHTML;
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
          target.innerHTML = originalHtml;
        }
      });
    });

    // Edit server config
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
          message: `确定要删除服务器 "${name || id}" (${id}) 吗？此操作将停止其连接并移除相关工具。`,
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

    // Toggle individual tool
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

    // View tool schema
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
    let transportType = existing?.transportType || 'stdio';
    if (transportType === 'http') {
      transportType = 'streamable_http';
    }

    const envLines = existing?.env
      ? Object.entries(existing.env)
          .map(([k, v]) => `${k}=${v}`)
          .join('\n')
      : '';

    const headerLines = existing?.headers
      ? Object.entries(existing.headers)
          .map(([k, v]) => `${k}: ${v}`)
          .join('\n')
      : '';

    const argsString = (existing?.args || []).join(' ');

    openDialog({
      title: isEdit ? `编辑 MCP 服务器: ${existing?.name}` : '添加 MCP 服务器',
      description: isEdit
        ? '修改传输方式或命令参数，支持可视化表单与 JSON 实时双向识别'
        : '配置外部 MCP Server，支持可视化表单与 JSON 实时双向识别',
      maxWidth: '40rem',
      bodyHtml: `
        <div class="flex flex-col gap-3 text-xs">
          <!-- Top Tabs & Bidirectional Sync Indicator -->
          <div class="flex items-center justify-between gap-2 pb-2 border-b border-border flex-wrap">
            <div class="tabs" id="mcp-dialog-tabs">
              <button type="button" class="tab-item active" data-tab="form">
                ${icon('sliders_horizontal', 'size-3.5')}
                <span>表单配置</span>
              </button>
              <button type="button" class="tab-item" data-tab="json">
                ${icon('file_code', 'size-3.5')}
                <span>JSON 编辑</span>
              </button>
            </div>
            <div id="mcp-sync-status" class="text-[11px] flex items-center gap-1.5 text-muted">
              <span class="inline-flex text-success">${icon('check', 'size-3')}</span>
              <span class="text-success">表单与 JSON 实时双向识别</span>
            </div>
          </div>

          <!-- Form Tab View -->
          <form id="mcp-tab-form" class="flex flex-col gap-4">
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
              <div class="flex items-center gap-4 mt-1 flex-wrap">
                <label class="flex items-center gap-1.5 cursor-pointer">
                  <input type="radio" name="form-transport" value="stdio" ${transportType === 'stdio' ? 'checked' : ''} />
                  <span>Stdio (本地子进程)</span>
                </label>
                <label class="flex items-center gap-1.5 cursor-pointer">
                  <input type="radio" name="form-transport" value="streamable_http" ${transportType === 'streamable_http' ? 'checked' : ''} />
                  <span>Streamable HTTP (2025-03 规范)</span>
                </label>
                <label class="flex items-center gap-1.5 cursor-pointer">
                  <input type="radio" name="form-transport" value="sse" ${transportType === 'sse' ? 'checked' : ''} />
                  <span>SSE (2024-11 规范)</span>
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
            <div id="http-fields" class="flex flex-col gap-3 p-3 rounded-md border border-border bg-muted/10 ${transportType !== 'stdio' ? '' : 'hidden'}">
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

          <!-- JSON Tab View -->
          <div id="mcp-tab-json" class="flex flex-col gap-2.5 hidden">
            <div class="flex items-center justify-between gap-2 flex-wrap">
              <span class="text-[11px] text-muted">
                支持直接粘贴 Claude Desktop <code>mcpServers</code>、标准 MCP 或单项配置，修改后实时同步。
              </span>
              <div class="flex items-center gap-1.5">
                <button type="button" class="btn btn-outline btn-sm" id="mcp-json-paste-btn" title="从剪贴板粘贴并识别" style="height: 1.625rem; padding: 0 0.5rem; font-size: 0.75rem;">
                  ${icon('upload', 'size-3')}
                  <span>粘贴</span>
                </button>
                <button type="button" class="btn btn-outline btn-sm" id="mcp-json-format-btn" title="格式化 JSON" style="height: 1.625rem; padding: 0 0.5rem; font-size: 0.75rem;">
                  ${icon('code', 'size-3')}
                  <span>格式化</span>
                </button>
                <button type="button" class="btn btn-outline btn-sm" id="mcp-json-copy-btn" title="复制 JSON 到剪贴板" style="height: 1.625rem; padding: 0 0.5rem; font-size: 0.75rem;">
                  ${icon('copy', 'size-3')}
                  <span>复制</span>
                </button>
              </div>
            </div>

            <textarea
              id="form-json"
              class="textarea w-full font-mono text-xs"
              rows="15"
              spellcheck="false"
              style="min-height: 18rem; font-size: 0.75rem; line-height: 1.5; tab-size: 2;"
              placeholder='{\n  "id": "my_server",\n  "name": "My MCP Server",\n  "transportType": "stdio",\n  "command": "npx",\n  "args": ["-y", "@modelcontextprotocol/server-everything"]\n}'
            ></textarea>

            <div id="mcp-json-alert" class="text-xs p-2 rounded-md hidden"></div>
          </div>
        </div>
      `,
      footerHtml: `
        <button class="btn btn-outline btn-sm dialog-close-btn">取消</button>
        <button class="btn btn-primary btn-sm" id="form-submit-btn">保存并应用</button>
      `,
      onMount: (dialogEl, close) => {
        const stdioFields = dialogEl.querySelector('#stdio-fields') as HTMLElement;
        const httpFields = dialogEl.querySelector('#http-fields') as HTMLElement;
        const submitBtn = dialogEl.querySelector('#form-submit-btn') as HTMLButtonElement;

        const formTabBtn = dialogEl.querySelector('#mcp-dialog-tabs [data-tab="form"]') as HTMLButtonElement;
        const jsonTabBtn = dialogEl.querySelector('#mcp-dialog-tabs [data-tab="json"]') as HTMLButtonElement;
        const formPanel = dialogEl.querySelector('#mcp-tab-form') as HTMLElement;
        const jsonPanel = dialogEl.querySelector('#mcp-tab-json') as HTMLElement;

        const formIdInput = dialogEl.querySelector('#form-id') as HTMLInputElement;
        const formNameInput = dialogEl.querySelector('#form-name') as HTMLInputElement;
        const formEnabledInput = dialogEl.querySelector('#form-enabled') as HTMLInputElement;
        const formCommandInput = dialogEl.querySelector('#form-command') as HTMLInputElement;
        const formArgsInput = dialogEl.querySelector('#form-args') as HTMLInputElement;
        const formWorkingDirInput = dialogEl.querySelector('#form-working-dir') as HTMLInputElement;
        const formEnvTextarea = dialogEl.querySelector('#form-env') as HTMLTextAreaElement;
        const formUrlInput = dialogEl.querySelector('#form-url') as HTMLInputElement;
        const formHeadersTextarea = dialogEl.querySelector('#form-headers') as HTMLTextAreaElement;

        const jsonTextarea = dialogEl.querySelector('#form-json') as HTMLTextAreaElement;
        const jsonAlert = dialogEl.querySelector('#mcp-json-alert') as HTMLElement;
        const syncStatus = dialogEl.querySelector('#mcp-sync-status') as HTMLElement;

        let isSyncing = false;
        let activeTab: 'form' | 'json' = 'form';

        function updateJSONFromForm(): void {
          const id = formIdInput.value.trim();
          const name = formNameInput.value.trim();
          const enabled = formEnabledInput.checked;
          const transport = (
            dialogEl.querySelector('input[name="form-transport"]:checked') as HTMLInputElement
          )?.value || 'stdio';

          const obj: Record<string, unknown> = {};
          if (id) obj.id = id;
          if (name) obj.name = name;
          obj.enabled = enabled;
          obj.transportType = transport;

          if (transport === 'stdio') {
            const command = formCommandInput.value.trim();
            const args = parseArgsText(formArgsInput.value);
            const workingDir = formWorkingDirInput.value.trim();
            const env = parseEnvText(formEnvTextarea.value);

            if (command) obj.command = command;
            if (args.length > 0) obj.args = args;
            if (workingDir) obj.workingDir = workingDir;
            if (Object.keys(env).length > 0) obj.env = env;
          } else {
            const url = formUrlInput.value.trim();
            const headers = parseHeadersText(formHeadersTextarea.value);

            if (url) obj.url = url;
            if (Object.keys(headers).length > 0) obj.headers = headers;
          }

          jsonTextarea.value = JSON.stringify(obj, null, 2);
          jsonAlert.className = 'hidden';
          syncStatus.innerHTML = `
            <span class="inline-flex text-success">${icon('check', 'size-3')}</span>
            <span class="text-success">表单与 JSON 实时双向识别</span>
          `;
        }

        function populateFormFromParsedObject(parsed: unknown): { success: boolean; error?: string; message?: string } {
          if (typeof parsed !== 'object' || parsed === null) {
            return { success: false, error: 'JSON 内容必须是一个对象' };
          }

          const parsedRecord = parsed as Record<string, unknown>;
          let target: Record<string, unknown> = parsedRecord;
          let inferredId = '';
          let note = '';

          // 1. Check if top-level mcpServers object (Claude Desktop / Cursor config)
          if (parsedRecord.mcpServers && typeof parsedRecord.mcpServers === 'object') {
            const serversObj = parsedRecord.mcpServers as Record<string, unknown>;
            const keys = Object.keys(serversObj);
            if (keys.length === 0) {
              return { success: false, error: 'mcpServers 对象为空' };
            }
            inferredId = keys[0];
            const firstServer = serversObj[keys[0]];
            if (typeof firstServer === 'object' && firstServer !== null) {
              target = firstServer as Record<string, unknown>;
            }
            if (keys.length > 1) {
              note = `识别到 ${keys.length} 个服务器，已载入首个 "${keys[0]}"`;
            }
          } else if (
            Object.keys(parsedRecord).length === 1 &&
            typeof Object.values(parsedRecord)[0] === 'object' &&
            Object.values(parsedRecord)[0] !== null
          ) {
            const singleVal = Object.values(parsedRecord)[0] as Record<string, unknown>;
            if (
              singleVal.command !== undefined ||
              singleVal.url !== undefined ||
              singleVal.transport !== undefined ||
              singleVal.args !== undefined
            ) {
              inferredId = Object.keys(parsedRecord)[0];
              target = singleVal;
            }
          }

          if (typeof target !== 'object' || target === null) {
            return { success: false, error: '未解析到有效的 MCP 配置对象' };
          }

          // ID
          const idVal = target.id || inferredId;
          if (idVal && !isEdit) {
            formIdInput.value = String(idVal);
          }

          // Name
          const nameVal = target.name || (idVal ? String(idVal) : '');
          if (nameVal) {
            formNameInput.value = String(nameVal);
          }

          // Enabled
          if (typeof target.enabled === 'boolean') {
            formEnabledInput.checked = target.enabled;
          }

          // Transport
          let transport = 'stdio';
          const tType = target.transportType || target.transport || target.type;
          if (typeof tType === 'string') {
            const lower = tType.toLowerCase();
            if (lower.includes('sse')) {
              transport = 'sse';
            } else if (lower.includes('http')) {
              transport = 'streamable_http';
            } else {
              transport = 'stdio';
            }
          } else if (target.url) {
            transport = String(target.url).toLowerCase().includes('sse') ? 'sse' : 'streamable_http';
          } else {
            transport = 'stdio';
          }

          const radio = dialogEl.querySelector(`input[name="form-transport"][value="${transport}"]`) as HTMLInputElement | null;
          if (radio) {
            radio.checked = true;
            if (transport === 'stdio') {
              stdioFields.classList.remove('hidden');
              httpFields.classList.add('hidden');
            } else {
              stdioFields.classList.add('hidden');
              httpFields.classList.remove('hidden');
            }
          }

          // Command
          if (target.command !== undefined) {
            formCommandInput.value = String(target.command || '');
          }

          // Args
          if (target.args !== undefined) {
            if (Array.isArray(target.args)) {
              formArgsInput.value = formatArgsArray(target.args.map(String));
            } else {
              formArgsInput.value = String(target.args || '');
            }
          }

          // Working Dir
          const workingDirVal = target.workingDir ?? target.working_dir ?? target.cwd;
          if (workingDirVal !== undefined) {
            formWorkingDirInput.value = String(workingDirVal || '');
          }

          // Env
          if (target.env !== undefined) {
            if (typeof target.env === 'object' && target.env !== null) {
              formEnvTextarea.value = formatEnvMap(target.env as Record<string, string>);
            } else {
              formEnvTextarea.value = String(target.env || '');
            }
          }

          // URL
          if (target.url !== undefined) {
            formUrlInput.value = String(target.url || '');
          }

          // Headers
          if (target.headers !== undefined) {
            if (typeof target.headers === 'object' && target.headers !== null) {
              formHeadersTextarea.value = formatHeadersMap(target.headers as Record<string, string>);
            } else {
              formHeadersTextarea.value = String(target.headers || '');
            }
          }

          return { success: true, message: note || '已识别并同步至表单' };
        }

        function updateFormFromJSON(): boolean {
          const text = jsonTextarea.value.trim();
          if (!text) {
            jsonAlert.className = 'hidden';
            syncStatus.innerHTML = `
              <span class="inline-flex text-muted">${icon('info', 'size-3')}</span>
              <span>等待输入 JSON 配置</span>
            `;
            return true;
          }

          const cleaned = cleanJSON(text);
          let parsed: unknown;
          try {
            parsed = JSON.parse(cleaned);
          } catch (e1) {
            if (!cleaned.startsWith('{') && !cleaned.startsWith('[')) {
              try {
                parsed = JSON.parse(`{${cleaned}}`);
              } catch {
                // Ignore fallback error
              }
            }
            if (!parsed) {
              const msg = e1 instanceof Error ? e1.message : String(e1);
              jsonAlert.className = 'text-xs p-2 rounded-md bg-destructive/10 text-destructive border border-destructive/20 block';
              jsonAlert.textContent = `JSON 语法错误: ${msg}`;
              syncStatus.innerHTML = `
                <span class="inline-flex text-destructive">${icon('circle_alert', 'size-3')}</span>
                <span class="text-destructive">JSON 语法错误</span>
              `;
              return false;
            }
          }

          const res = populateFormFromParsedObject(parsed);
          if (res.success) {
            jsonAlert.className = 'hidden';
            syncStatus.innerHTML = `
              <span class="inline-flex text-success">${icon('check', 'size-3')}</span>
              <span class="text-success">${res.message || '已成功识别并同步至表单'}</span>
            `;
            return true;
          } else {
            jsonAlert.className = 'text-xs p-2 rounded-md bg-destructive/10 text-destructive border border-destructive/20 block';
            jsonAlert.textContent = res.error || '未识别到有效的 MCP 配置';
            syncStatus.innerHTML = `
              <span class="inline-flex text-destructive">${icon('circle_alert', 'size-3')}</span>
              <span class="text-destructive">${res.error || '配置无法识别'}</span>
            `;
            return false;
          }
        }

        // Initialize JSON from initial form state
        isSyncing = true;
        try {
          updateJSONFromForm();
        } finally {
          isSyncing = false;
        }

        // Transport radio change listener
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
            if (isSyncing) return;
            isSyncing = true;
            try {
              updateJSONFromForm();
            } finally {
              isSyncing = false;
            }
          });
        });

        // Listen for all input/change events on form fields to update JSON
        const formInputs = dialogEl.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>(
          '#form-id, #form-name, #form-enabled, #form-command, #form-args, #form-working-dir, #form-env, #form-url, #form-headers'
        );
        formInputs.forEach((input) => {
          const handler = () => {
            if (isSyncing) return;
            isSyncing = true;
            try {
              updateJSONFromForm();
            } finally {
              isSyncing = false;
            }
          };
          input.addEventListener('input', handler);
          input.addEventListener('change', handler);
        });

        // Listen for input on JSON textarea to update form
        jsonTextarea.addEventListener('input', () => {
          if (isSyncing) return;
          isSyncing = true;
          try {
            updateFormFromJSON();
          } finally {
            isSyncing = false;
          }
        });

        // Tab Switching
        formTabBtn.addEventListener('click', () => {
          if (activeTab === 'form') return;
          if (isSyncing) return;
          isSyncing = true;
          let ok = true;
          try {
            ok = updateFormFromJSON();
          } finally {
            isSyncing = false;
          }
          if (!ok) {
            toast.error('当前 JSON 存在语法错误，请先修正后再切换到表单视图');
            return;
          }
          activeTab = 'form';
          formTabBtn.classList.add('active');
          jsonTabBtn.classList.remove('active');
          jsonPanel.classList.add('hidden');
          formPanel.classList.remove('hidden');
        });

        jsonTabBtn.addEventListener('click', () => {
          if (activeTab === 'json') return;
          if (isSyncing) return;
          isSyncing = true;
          try {
            updateJSONFromForm();
          } finally {
            isSyncing = false;
          }
          activeTab = 'json';
          jsonTabBtn.classList.add('active');
          formTabBtn.classList.remove('active');
          formPanel.classList.add('hidden');
          jsonPanel.classList.remove('hidden');
          jsonTextarea.focus();
        });

        // Format JSON button
        dialogEl.querySelector('#mcp-json-format-btn')?.addEventListener('click', () => {
          const text = jsonTextarea.value.trim();
          if (!text) return;
          try {
            const cleaned = cleanJSON(text);
            let parsed: unknown;
            try {
              parsed = JSON.parse(cleaned);
            } catch {
              if (!cleaned.startsWith('{') && !cleaned.startsWith('[')) {
                parsed = JSON.parse(`{${cleaned}}`);
              }
            }
            if (parsed && typeof parsed === 'object') {
              jsonTextarea.value = JSON.stringify(parsed, null, 2);
              toast.success('JSON 已格式化');
            } else {
              toast.error('JSON 语法错误，无法格式化');
            }
          } catch {
            toast.error('JSON 语法错误，无法格式化');
          }
        });

        // Copy JSON button
        dialogEl.querySelector('#mcp-json-copy-btn')?.addEventListener('click', async () => {
          const text = jsonTextarea.value;
          if (!text) return;
          await copyToClipboard(text);
          toast.success('JSON 已复制到剪贴板');
        });

        // Paste JSON button
        dialogEl.querySelector('#mcp-json-paste-btn')?.addEventListener('click', async () => {
          try {
            const text = await navigator.clipboard.readText();
            if (!text.trim()) {
              toast.error('剪贴板中没有内容');
              return;
            }
            jsonTextarea.value = text;
            if (isSyncing) return;
            isSyncing = true;
            try {
              const ok = updateFormFromJSON();
              if (ok) {
                toast.success('已粘贴并识别 MCP 配置');
              } else {
                toast.error('已粘贴，但未识别到有效的 MCP 配置');
              }
            } finally {
              isSyncing = false;
            }
          } catch {
            toast.error('读取剪贴板失败，请在输入框中直接使用 Ctrl+V 粘贴');
          }
        });

        submitBtn.addEventListener('click', async () => {
          // If currently in JSON tab, make sure form is updated from JSON
          if (activeTab === 'json') {
            if (isSyncing) return;
            isSyncing = true;
            let ok = true;
            try {
              ok = updateFormFromJSON();
            } finally {
              isSyncing = false;
            }
            if (!ok) {
              toast.error('JSON 语法有误，请先修正后再保存');
              return;
            }
          }

          const id = formIdInput.value.trim();
          const name = formNameInput.value.trim();
          const enabled = formEnabledInput.checked;
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
            command = formCommandInput.value.trim();
            if (!command) {
              toast.error('请输入执行命令');
              return;
            }
            args = parseArgsText(formArgsInput.value);
            workingDir = formWorkingDirInput.value.trim();
            const envRaw = formEnvTextarea.value.trim();
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
            url = formUrlInput.value.trim();
            if (!url) {
              toast.error('请输入服务器 URL');
              return;
            }
            if (!url.startsWith('http://') && !url.startsWith('https://')) {
              toast.error('服务器 URL 必须以 http:// 或 https:// 开头');
              return;
            }
            const headersRaw = formHeadersTextarea.value.trim();
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
