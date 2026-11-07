import { createInstanceAPI } from '../api/client';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { escapeHtml } from '../utils/formatters';

function isWordPrefix(prefix: string): boolean {
  const trimmed = prefix.trim();
  if (!trimmed) return false;
  const lastChar = trimmed[trimmed.length - 1];
  return /^[a-zA-Z0-9_一-龥]$/.test(lastChar);
}

function getPrefixParts(prefix: string): { prefix: string; sep: string } {
  const trimmed = prefix.trim() || '/';
  const sep = isWordPrefix(trimmed) ? ' ' : '';
  return { prefix: trimmed, sep };
}

export function mountCommandSettingsPage(container: HTMLElement, api = createInstanceAPI()): () => void {
  let isUnmounted = false;
  let loaded = false;
  let loading = false;
  let saving = false;
  const controller = new AbortController();

  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="flex items-center justify-between gap-4 flex-wrap pb-1">
        <div class="flex items-center gap-2.5">
          <a href="#/settings" class="btn btn-ghost btn-icon-sm" title="返回设置" style="text-decoration: none;">
            ${icon('arrow_left', 'w-4 h-4')}
          </a>
          <div>
            <h1 class="page-title">指令设置</h1>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <button class="btn btn-outline btn-sm" id="cmd-reset-default-btn" disabled>
            ${icon('refresh', 'w-3.5 h-3.5')}
            <span>重置指令前缀 (/)</span>
          </button>
          <button class="btn btn-primary btn-sm" id="cmd-save-btn" disabled>
            ${icon('save', 'w-3.5 h-3.5')}
            <span>保存设置</span>
          </button>
        </div>
      </header>

      <div class="flex flex-col gap-4" style="max-width: 48rem;">
        <div class="flex items-center gap-3" id="cmd-load-status" role="status">
          <span id="cmd-load-message" class="text-xs text-muted">正在加载指令设置…</span>
          <button class="btn btn-outline btn-sm" id="cmd-retry-btn" hidden disabled>重新加载</button>
        </div>
        <article class="card p-5 flex flex-col gap-3">
          <div class="flex items-center justify-between gap-3">
            <label for="cmd-runtime-status" class="text-sm font-semibold text-foreground">输出FrostAgent默认运行信息</label>
            <label class="toggle-switch">
              <input type="checkbox" id="cmd-runtime-status" role="switch" aria-label="输出FrostAgent默认运行信息" aria-describedby="cmd-runtime-status-description" disabled />
              <span aria-hidden="true"></span>
            </label>
          </div>
          <p id="cmd-runtime-status-description" class="text-xs text-muted leading-relaxed">一旦输入 FrostAgent 预留的调试语句“#fa”，将会促使 Bot 输出基本运行信息。</p>
          <div class="flex items-center gap-2">
            <span class="badge badge-outline">当前实例</span>
            <span class="badge badge-outline">默认开启</span>
            <span class="badge badge-outline">保存后立即生效</span>
          </div>
        </article>
        <!-- Prefix Config Card -->
        <article class="card p-5 flex flex-col gap-4">
          <div class="flex flex-col gap-1.5">
            <label class="form-label font-semibold text-foreground text-sm" for="cmd-prefix-input">
              指令前缀 (ADMIN_COMMAND_PREFIX)
            </label>
            <div class="flex items-center gap-3">
              <input
                type="text"
                id="cmd-prefix-input"
                class="input font-mono text-sm"
                placeholder="/"
                style="max-width: 16rem;"
                disabled
              />
              <span class="badge badge-outline">当前实例</span>
              <span class="badge badge-outline">立即生效</span>
            </div>
            <p class="text-xs text-muted leading-relaxed mt-1">
              支持符号前缀（如 <code>/</code>、<code>!</code>）或单词前缀（如 <code>execute</code>）。<br/>
              单词前缀必须以空格与指令名称分隔；符号前缀支持紧贴或空格分隔。保存后持久化于当前实例的数据库设置中并立即生效。
            </p>
          </div>
        </article>

        <!-- Command Usage Examples Card -->
        <article class="card p-5 flex flex-col gap-3.5">
          <div class="flex items-center justify-between">
            <h2 class="text-sm font-semibold text-foreground flex items-center gap-2">
              ${icon('terminal', 'w-4 h-4 text-primary')}
              <span>动态指令语法与使用示例</span>
            </h2>
            <span class="text-xs text-muted">必须在真实 @ 机器人的消息中触发</span>
          </div>

          <div class="flex flex-col gap-2.5 mt-1" id="cmd-examples-list">
            <!-- Dynamically populated -->
          </div>
        </article>
      </div>
    </div>
  `;

  const prefixInput = container.querySelector<HTMLInputElement>('#cmd-prefix-input')!;
  const runtimeStatusInput = container.querySelector<HTMLInputElement>('#cmd-runtime-status')!;
  const saveBtn = container.querySelector<HTMLButtonElement>('#cmd-save-btn')!;
  const resetDefaultBtn = container.querySelector<HTMLButtonElement>('#cmd-reset-default-btn')!;
  const examplesList = container.querySelector<HTMLElement>('#cmd-examples-list')!;
  const loadStatus = container.querySelector<HTMLElement>('#cmd-load-status')!;
  const loadMessage = container.querySelector<HTMLElement>('#cmd-load-message')!;
  const retryBtn = container.querySelector<HTMLButtonElement>('#cmd-retry-btn')!;

  function syncControls() {
    const disabled = !loaded || loading || saving || isUnmounted;
    prefixInput.disabled = disabled;
    runtimeStatusInput.disabled = disabled;
    saveBtn.disabled = disabled;
    resetDefaultBtn.disabled = disabled;
    retryBtn.disabled = loading || saving || isUnmounted;
  }

  function renderExamples(prefix: string) {
    const { prefix: p, sep } = getPrefixParts(prefix);

    const commands = [
      {
        name: 'reset',
        usage: `${p}${sep}reset`,
        desc: '重置当前会话与上下文',
        detail: '中断会话正在进行的模型生成，清空历史对话、滚动总结、未压缩群聊/私聊缓冲区及待提取记忆，并物理删除磁盘持久化总结。',
      },
      {
        name: 'ban',
        usage: `${p}${sep}ban <userID>`,
        desc: '全局安全封禁用户',
        detail: '在全局安全控制器中锁定指定用户（严格以 "Admin ban" 为原因），自动防止封禁管理员自身或配置的管理员账号。',
      },
      {
        name: 'unban',
        usage: `${p}${sep}unban <userID>`,
        desc: '解除用户全局安全封禁',
        detail: '在全局安全控制器中解锁指定用户账号，恢复其正常交互权限。',
      },
      {
        name: 'compact',
        usage: `${p}${sep}compact`,
        desc: '立即压缩总结当前会话上下文',
        detail: '跳过缓冲区条数和触发时间冷却限制，立即发起群聊或私聊上下文压缩，返回执行状态并在完成后回执。',
      },
      {
        name: 'reflect',
        usage: `${p}${sep}reflect`,
        desc: '触发记忆反思与提炼任务',
        detail: '针对当前会话主体（群聊或私聊）触发后台记忆反思提炼，具备并发防护，在开始与完成时分别发送回执。',
      },
    ];

    examplesList.innerHTML = commands
      .map(
        (cmd) => `
        <div class="p-3.5 rounded-md bg-muted/40 border border-border flex flex-col gap-1.5 transition-all">
          <div class="flex items-center justify-between gap-2 flex-wrap">
            <div class="flex items-center gap-2">
              <code class="px-2 py-0.5 rounded bg-muted font-mono text-xs font-semibold text-primary">
                @Bot ${escapeHtml(cmd.usage)}
              </code>
              <span class="text-xs font-medium text-foreground">${escapeHtml(cmd.desc)}</span>
            </div>
            <span class="badge badge-outline text-[11px] font-mono">${escapeHtml(cmd.name)}</span>
          </div>
          <p class="text-xs text-muted leading-relaxed">${escapeHtml(cmd.detail)}</p>
        </div>
      `,
      )
      .join('');
  }

  prefixInput.addEventListener('input', () => {
    renderExamples(prefixInput.value);
  });

  resetDefaultBtn.addEventListener('click', () => {
    prefixInput.value = '/';
    renderExamples('/');
  });

  async function loadData() {
    if (isUnmounted || loading || saving) return;
    loading = true;
    loaded = false;
    loadStatus.hidden = false;
    loadMessage.textContent = '正在加载指令设置…';
    retryBtn.hidden = true;
    syncControls();
    try {
      const vars = await api.listEnvVars();
      if (isUnmounted) return;
      const found = vars.find((v) => v.key === 'ADMIN_COMMAND_PREFIX');
      prefixInput.value = found?.value?.trim() || '/';
      runtimeStatusInput.checked = vars.find((v) => v.key === 'ENABLE_RUNTIME_STATUS_COMMAND')?.value !== 'false';
      renderExamples(prefixInput.value);
      loaded = true;
      loadStatus.hidden = true;
    } catch (err) {
      if (isUnmounted) return;
      toast.error('加载指令设置失败: ' + (err instanceof Error ? err.message : String(err)));
      loadMessage.textContent = '加载失败，请重新加载后再修改设置。';
      retryBtn.hidden = false;
    } finally {
      loading = false;
      if (!isUnmounted) syncControls();
    }
  }

  async function saveData() {
    if (!loaded || loading || saving || isUnmounted) return;
    const newPrefix = prefixInput.value.trim() || '/';
    const runtimeStatusEnabled = runtimeStatusInput.checked;
    saving = true;
    syncControls();

    try {
      const res = await api.updateEnvVars([
        { key: 'ADMIN_COMMAND_PREFIX', value: newPrefix, isSecret: false },
        { key: 'ENABLE_RUNTIME_STATUS_COMMAND', value: String(runtimeStatusEnabled), isSecret: false },
      ], controller.signal);

      if (isUnmounted) return;
      if (res.success) {
        prefixInput.value = newPrefix;
        runtimeStatusInput.checked = runtimeStatusEnabled;
        renderExamples(newPrefix);
        toast.success('指令设置已保存并立即生效');
      } else {
        toast.error('保存指令设置失败: ' + (res.error || '未知错误'));
      }
    } catch (err) {
      if (isUnmounted) return;
      toast.error('保存指令设置失败: ' + (err instanceof Error ? err.message : String(err)));
    } finally {
      if (!isUnmounted) {
        saving = false;
        syncControls();
      }
    }
  }

  saveBtn.addEventListener('click', () => void saveData());
  retryBtn.addEventListener('click', () => void loadData());

  void loadData();

  return () => {
    isUnmounted = true;
    controller.abort();
  };
}
