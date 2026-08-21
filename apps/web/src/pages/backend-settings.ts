import { api } from '../api/client';
import { EnvVar } from '@frostagent/proto';
import { escapeHtml, maskSecret } from '../utils/formatters';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { openDialog } from '../components/dialog';
import { confirmDialog } from '../components/confirm';

export function mountBackendSettingsPage(container: HTMLElement): () => void {
  let isUnmounted = false;
  let loading = false;
  let saving = false;
  let envVars: EnvVar[] = [];
  let rawContent = '';
  const visibleSecrets = new Set<string>();
  let editingKey: string | null = null;
  let editingValue = '';
  let editingIsSecret = false;

  let groupReplyOnMention = false;
  let enableAtOther = false;
  let enableReplyOther = false;

  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="flex items-center justify-between gap-4 flex-wrap pb-1">
        <div class="flex items-center gap-2.5">
          <a href="#/settings" class="btn btn-ghost btn-icon-sm" title="返回设置" style="text-decoration: none;">
            ${icon('arrow_left', 'w-4 h-4')}
          </a>
          <div>
            <h1 class="page-title">Bot 服务端设置</h1>
            <p class="page-description">修改服务端运行环境变量与群聊响应策略</p>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <button class="btn btn-primary btn-sm" id="add-env-btn">
            ${icon('plus', 'w-3.5 h-3.5')}
            <span>新增环境变量</span>
          </button>
          <button class="btn btn-outline btn-icon-sm" id="backend-refresh-btn" title="刷新">
            ${icon('refresh', 'w-3.5 h-3.5')}
          </button>
        </div>
      </header>

      <!-- Tabs -->
      <div class="tabs">
        <button class="tab-item active" id="tab-table-btn">
          ${icon('table', 'w-3.5 h-3.5')}
          <span>环境变量表</span>
        </button>
        <button class="tab-item" id="tab-raw-btn">
          ${icon('file_text', 'w-3.5 h-3.5')}
          <span>原始 .env</span>
        </button>
      </div>

      <!-- Table View Container -->
      <div id="tab-table-content" class="flex flex-col gap-4">
        <!-- Group Behavior Settings Card -->
        <article class="card p-3.5">
          <div class="card-header border-b border-border pb-2.5 mb-2.5">
            <div class="flex items-center gap-2">
              <span class="text-primary flex items-center">${icon('users', 'w-4 h-4')}</span>
              <h2 class="card-title text-sm font-semibold">群聊回复行为策略</h2>
            </div>
          </div>
          <div class="card-content grid grid-cols-1 sm:grid-cols-3 gap-2.5">
            <label class="card p-2.5 flex items-start gap-2.5 cursor-pointer hover-bg transition-colors">
              <input type="checkbox" id="group-mention-cb" class="checkbox" style="margin-top: 0.125rem;" />
              <div>
                <span class="text-xs font-semibold text-foreground">被 @ 时触发回复</span>
                <p class="text-[11px] text-muted font-mono mt-0.5">GROUP_REPLY_ON_MENTION</p>
              </div>
            </label>

            <label class="card p-2.5 flex items-start gap-2.5 cursor-pointer hover-bg transition-colors">
              <input type="checkbox" id="group-at-cb" class="checkbox" style="margin-top: 0.125rem;" />
              <div>
                <span class="text-xs font-semibold text-foreground">回复时 @ 对方</span>
                <p class="text-[11px] text-muted font-mono mt-0.5">ENABLE_AT_IN_GROUP_MSG</p>
              </div>
            </label>

            <label class="card p-2.5 flex items-start gap-2.5 cursor-pointer hover-bg transition-colors">
              <input type="checkbox" id="group-reply-cb" class="checkbox" style="margin-top: 0.125rem;" />
              <div>
                <span class="text-xs font-semibold text-foreground">引用/回复对方消息</span>
                <p class="text-[11px] text-muted font-mono mt-0.5">ENABLE_REPLY_IN_GROUP_MSG</p>
              </div>
            </label>
          </div>
        </article>

        <!-- Env Vars Table Card -->
        <div class="card overflow-hidden">
          <div class="table-container">
            <table class="table">
              <thead>
                <tr>
                  <th style="width: 18rem;">变量名 (Key)</th>
                  <th>变量值 (Value)</th>
                  <th style="width: 6rem; text-align: right;">操作</th>
                </tr>
              </thead>
              <tbody id="env-table-body">
                <tr>
                  <td colspan="3" class="text-center text-muted" style="padding: 2.5rem;">
                    <span class="spinner"></span>
                    <span style="margin-left: 0.5rem;">加载中...</span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>

      <!-- Raw .env View Container -->
      <div id="tab-raw-content" class="flex flex-col gap-3" style="display: none;">
        <article class="card p-3.5 flex flex-col gap-3">
          <div class="flex items-center justify-between">
            <label class="form-label" for="raw-env-textarea">.env 原始文件编辑</label>
            <button class="btn btn-primary btn-sm" id="save-raw-env-btn">
              ${icon('save', 'w-3.5 h-3.5')}
              <span>保存 .env 文件</span>
            </button>
          </div>
          <textarea
            id="raw-env-textarea"
            class="textarea font-mono text-xs leading-relaxed"
            rows="20"
            placeholder="KEY=VALUE..."
            style="white-space: pre;"
          ></textarea>
        </article>
      </div>
    </div>
  `;

  // Elements
  const tabTableBtn = container.querySelector<HTMLButtonElement>('#tab-table-btn')!;
  const tabRawBtn = container.querySelector<HTMLButtonElement>('#tab-raw-btn')!;
  const tabTableContent = container.querySelector<HTMLElement>('#tab-table-content')!;
  const tabRawContent = container.querySelector<HTMLElement>('#tab-raw-content')!;

  const refreshBtn = container.querySelector<HTMLButtonElement>('#backend-refresh-btn')!;
  const addEnvBtn = container.querySelector<HTMLButtonElement>('#add-env-btn')!;

  const groupMentionCb = container.querySelector<HTMLInputElement>('#group-mention-cb')!;
  const groupAtCb = container.querySelector<HTMLInputElement>('#group-at-cb')!;
  const groupReplyCb = container.querySelector<HTMLInputElement>('#group-reply-cb')!;

  const tbody = container.querySelector<HTMLElement>('#env-table-body')!;
  const rawTextarea = container.querySelector<HTMLTextAreaElement>('#raw-env-textarea')!;
  const saveRawEnvBtn = container.querySelector<HTMLButtonElement>('#save-raw-env-btn')!;

  async function loadData() {
    if (isUnmounted) return;
    loading = true;
    renderTable();

    try {
      const [vars, raw] = await Promise.all([api.listEnvVars(), api.getRawEnvFile()]);
      if (isUnmounted) return;
      envVars = vars;
      rawContent = raw;
      rawTextarea.value = rawContent;

      const getVal = (k: string) => vars.find((v) => v.key === k)?.value ?? '';
      groupReplyOnMention = getVal('GROUP_REPLY_ON_MENTION') !== 'false';
      enableAtOther = getVal('ENABLE_AT_IN_GROUP_MSG') === 'true';
      enableReplyOther = getVal('ENABLE_REPLY_IN_GROUP_MSG') === 'true';

      groupMentionCb.checked = groupReplyOnMention;
      groupAtCb.checked = enableAtOther;
      groupReplyCb.checked = enableReplyOther;
    } catch (err) {
      if (isUnmounted) return;
      toast.error('加载环境变量失败: ' + (err instanceof Error ? err.message : String(err)));
      envVars = [];
    } finally {
      if (!isUnmounted) {
        loading = false;
        renderTable();
      }
    }
  }

  function renderTable() {
    if (loading && envVars.length === 0) {
      tbody.innerHTML = `
        <tr>
          <td colspan="3" class="text-center text-muted" style="padding: 2.5rem;">
            <span class="spinner"></span>
            <span style="margin-left: 0.5rem;">加载中...</span>
          </td>
        </tr>
      `;
      return;
    }

    if (envVars.length === 0) {
      tbody.innerHTML = `
        <tr>
          <td colspan="3" class="text-center text-muted" style="padding: 3rem;">
            暂无环境变量配置。
          </td>
        </tr>
      `;
      return;
    }

    tbody.innerHTML = envVars
      .map((item) => {
        const isEditing = editingKey === item.key;
        const isSecret = item.isSecret;
        const isVisible = visibleSecrets.has(item.key);

        if (isEditing) {
          return `
            <tr class="bg-muted">
              <td>
                <span class="font-mono text-xs font-semibold text-foreground">${escapeHtml(item.key)}</span>
              </td>
              <td>
                <div class="flex items-center gap-2">
                  <input
                    type="${editingIsSecret ? 'password' : 'text'}"
                    class="input font-mono text-xs"
                    id="edit-env-val-input"
                    value="${escapeHtml(editingValue)}"
                    style="height: 1.875rem;"
                  />
                  <label class="flex items-center gap-1.5 cursor-pointer text-xs select-none" style="white-space: nowrap;">
                    <input type="checkbox" id="edit-env-secret-cb" class="checkbox" ${editingIsSecret ? 'checked' : ''} />
                    <span class="text-muted">敏感</span>
                  </label>
                </div>
              </td>
              <td style="text-align: right;">
                <div class="flex items-center justify-end gap-1">
                  <button class="btn btn-primary btn-icon-sm" style="width: 1.75rem; height: 1.75rem;" id="save-inline-btn" title="保存">
                    ${icon('check', 'w-3.5 h-3.5')}
                  </button>
                  <button class="btn btn-outline btn-icon-sm" style="width: 1.75rem; height: 1.75rem;" id="cancel-inline-btn" title="取消">
                    ${icon('close', 'w-3.5 h-3.5')}
                  </button>
                </div>
              </td>
            </tr>
          `;
        }

        const displayVal = isSecret && !isVisible ? maskSecret(item.value) : item.value;

        return `
          <tr>
            <td>
              <div class="flex items-center gap-1.5">
                ${isSecret ? `<span class="text-muted flex items-center" title="敏感配置">${icon('lock', 'w-3.5 h-3.5')}</span>` : ''}
                <span class="font-mono text-xs font-medium select-text text-foreground">${escapeHtml(item.key)}</span>
              </div>
            </td>
            <td>
              <div class="flex items-center gap-1.5">
                <span class="font-mono text-xs break-all select-text text-foreground">${escapeHtml(displayVal || '（空）')}</span>
                ${
                  isSecret
                    ? `
                  <button class="btn btn-ghost btn-icon-sm text-muted" style="width: 1.5rem; height: 1.5rem; padding: 0;" data-action="toggle-secret" data-key="${escapeHtml(
                    item.key,
                  )}" title="${isVisible ? '隐藏' : '显示'}">
                    ${icon(isVisible ? 'eye_off' : 'eye', 'w-3 h-3')}
                  </button>
                `
                    : ''
                }
              </div>
            </td>
            <td style="text-align: right;">
              <div class="flex items-center justify-end gap-1">
                <button class="btn btn-ghost btn-icon-sm" style="width: 1.75rem; height: 1.75rem;" data-action="edit-env" data-key="${escapeHtml(
                  item.key,
                )}" title="修改">
                  ${icon('pencil', 'w-3.5 h-3.5')}
                </button>
                <button class="btn btn-ghost btn-icon-sm text-destructive" style="width: 1.75rem; height: 1.75rem;" data-action="delete-env" data-key="${escapeHtml(
                  item.key,
                )}" title="删除">
                  ${icon('trash', 'w-3.5 h-3.5')}
                </button>
              </div>
            </td>
          </tr>
        `;
      })
      .join('');

    // Attach inline edit handlers if active
    if (editingKey) {
      const editInput = tbody.querySelector<HTMLInputElement>('#edit-env-val-input');
      const editSecretCb = tbody.querySelector<HTMLInputElement>('#edit-env-secret-cb');
      const saveInlineBtn = tbody.querySelector<HTMLButtonElement>('#save-inline-btn');
      const cancelInlineBtn = tbody.querySelector<HTMLButtonElement>('#cancel-inline-btn');

      editInput?.focus();
      editInput?.addEventListener('input', () => {
        editingValue = editInput.value;
      });
      editSecretCb?.addEventListener('change', () => {
        editingIsSecret = editSecretCb.checked;
        if (editInput) editInput.type = editingIsSecret ? 'password' : 'text';
      });

      saveInlineBtn?.addEventListener('click', async () => {
        await saveEnvVar(editingKey!, editingValue, editingIsSecret);
        editingKey = null;
        renderTable();
      });

      cancelInlineBtn?.addEventListener('click', () => {
        editingKey = null;
        renderTable();
      });
    }

    // Attach row button events
    tbody.querySelectorAll<HTMLButtonElement>('[data-action="toggle-secret"]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const key = btn.dataset.key;
        if (!key) return;
        if (visibleSecrets.has(key)) visibleSecrets.delete(key);
        else visibleSecrets.add(key);
        renderTable();
      });
    });

    tbody.querySelectorAll<HTMLButtonElement>('[data-action="edit-env"]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const key = btn.dataset.key;
        const item = envVars.find((v) => v.key === key);
        if (item) {
          editingKey = item.key;
          editingValue = item.value;
          editingIsSecret = item.isSecret;
          renderTable();
        }
      });
    });

    tbody.querySelectorAll<HTMLButtonElement>('[data-action="delete-env"]').forEach((btn) => {
      btn.addEventListener('click', async () => {
        const key = btn.dataset.key;
        if (!key) return;
        const confirmed = await confirmDialog({
          title: '删除环境变量',
          message: `确认删除环境变量 ${key} 吗？`,
          confirmLabel: '删除',
          destructive: true,
        });
        if (confirmed) {
          try {
            const res = await api.deleteEnvVar(key);
            if (res.success) {
              toast.success('环境变量已删除');
              void loadData();
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

  async function saveEnvVar(key: string, value: string, isSecret: boolean) {
    if (!key) return;
    try {
      const res = await api.updateEnvVar({ key, value, isSecret });
      if (res.success) {
        toast.success('环境变量已保存');
        void loadData();
      } else {
        toast.error('保存失败: ' + res.error);
      }
    } catch (err) {
      toast.error('保存失败: ' + (err instanceof Error ? err.message : String(err)));
    }
  }

  function openAddEnvModal() {
    openDialog({
      title: '新增环境变量',
      description: '添加或覆盖服务端使用的环境变量。',
      maxWidth: '32rem',
      bodyHtml: `
        <div class="flex flex-col gap-3.5">
          <div class="form-group">
            <label class="form-label" for="add-env-key">Key <span class="text-destructive">*</span></label>
            <input id="add-env-key" class="input font-mono text-xs" placeholder="如 MODEL_NAME, API_KEY..." autocomplete="off" />
          </div>
          <div class="form-group">
            <label class="form-label" for="add-env-val">Value</label>
            <input id="add-env-val" class="input font-mono text-xs" placeholder="环境变量值..." autocomplete="off" />
          </div>
          <label class="flex items-center gap-2 cursor-pointer text-xs select-none">
            <input type="checkbox" id="add-env-secret-cb" class="checkbox" />
            <span class="text-muted">这是敏感信息（自动脱敏显示）</span>
          </label>
        </div>
      `,
      footerHtml: `
        <button class="btn btn-outline btn-sm" id="add-env-cancel">取消</button>
        <button class="btn btn-primary btn-sm" id="add-env-save">
          ${icon('save', 'w-3.5 h-3.5')}
          <span>保存</span>
        </button>
      `,
      onMount: (dialogEl, close) => {
        const keyInput = dialogEl.querySelector<HTMLInputElement>('#add-env-key')!;
        const valInput = dialogEl.querySelector<HTMLInputElement>('#add-env-val')!;
        const secretCb = dialogEl.querySelector<HTMLInputElement>('#add-env-secret-cb')!;
        const saveBtn = dialogEl.querySelector<HTMLButtonElement>('#add-env-save')!;
        const cancelBtn = dialogEl.querySelector<HTMLButtonElement>('#add-env-cancel')!;

        secretCb.addEventListener('change', () => {
          valInput.type = secretCb.checked ? 'password' : 'text';
        });

        cancelBtn.addEventListener('click', () => close());
        saveBtn.addEventListener('click', async () => {
          const key = keyInput.value.trim();
          if (!key) {
            toast.error('Key 不能为空');
            return;
          }
          const value = valInput.value;
          const isSecret = secretCb.checked;

          try {
            saveBtn.disabled = true;
            const res = await api.updateEnvVar({ key, value, isSecret });
            if (res.success) {
              toast.success('环境变量已保存');
              close();
              void loadData();
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

  // Toggle behavior settings
  async function toggleGroupSetting(key: string, value: boolean) {
    try {
      const res = await api.updateEnvVar({
        key,
        value: value ? 'true' : 'false',
        isSecret: false,
      });
      if (res.success) {
        toast.success('群聊设置已更新');
        void loadData();
      } else {
        toast.error('更新失败: ' + res.error);
      }
    } catch (err) {
      toast.error('更新失败: ' + (err instanceof Error ? err.message : String(err)));
    }
  }

  // Raw .env save
  async function saveRawEnv() {
    if (saving) return;
    saving = true;
    saveRawEnvBtn.disabled = true;
    try {
      const content = rawTextarea.value;
      const res = await api.updateRawEnvFile(content);
      if (res.success) {
        toast.success('.env 文件已更新并已重载配置');
        void loadData();
      } else {
        toast.error('更新失败: ' + res.error);
      }
    } catch (err) {
      toast.error('更新失败: ' + (err instanceof Error ? err.message : String(err)));
    } finally {
      saving = false;
      saveRawEnvBtn.disabled = false;
    }
  }

  // Tab switching
  tabTableBtn.addEventListener('click', () => {
    tabTableBtn.classList.add('active');
    tabRawBtn.classList.remove('active');
    tabTableContent.style.display = 'flex';
    tabRawContent.style.display = 'none';
  });

  tabRawBtn.addEventListener('click', () => {
    tabTableBtn.classList.remove('active');
    tabRawBtn.classList.add('active');
    tabTableContent.style.display = 'none';
    tabRawContent.style.display = 'flex';
  });

  // Group cb handlers
  groupMentionCb.addEventListener('change', () => void toggleGroupSetting('GROUP_REPLY_ON_MENTION', groupMentionCb.checked));
  groupAtCb.addEventListener('change', () => void toggleGroupSetting('ENABLE_AT_IN_GROUP_MSG', groupAtCb.checked));
  groupReplyCb.addEventListener('change', () => void toggleGroupSetting('ENABLE_REPLY_IN_GROUP_MSG', groupReplyCb.checked));

  addEnvBtn.addEventListener('click', openAddEnvModal);
  saveRawEnvBtn.addEventListener('click', () => void saveRawEnv());
  refreshBtn.addEventListener('click', () => void loadData());

  void loadData();

  return () => {
    isUnmounted = true;
  };
}
