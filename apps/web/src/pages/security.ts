import {
  securityAPI,
  type LockedPrincipal,
  type SecurityControlMode,
} from '../api/client';
import { escapeHtml, formatDateTime } from '../utils/formatters';
import { toast } from '../components/toast';
import { icon } from '../components/icons';

interface ModeOption {
  value: SecurityControlMode;
  label: string;
  badge?: string;
  desc: string;
}

const MODE_OPTIONS: ModeOption[] = [
  {
    value: 'off',
    label: '无',
    desc: '关闭所有语义安全审查，所有文本及工具不经网关模型，自主封禁工具失效。',
  },
  {
    value: 'simple',
    label: '简单模式',
    badge: '默认模式',
    desc: '默认模式。跳过常规文本和工具调用审查，零审查模型消耗，但智能体拥有自主封禁能力（ban_user 可用），且管理员指令与封禁库持续生效。',
  },
  {
    value: 'aggressive',
    label: '激进模式',
    desc: '开启全流程语义审查，对用户输入、上下文、工具调用与模型回复执行高严格度审查。',
  },
];

export function mountSecurityPage(container: HTMLElement): () => void {
  let disposed = false;
  let records: LockedPrincipal[] = [];
  let currentMode: SecurityControlMode = 'simple';
  let isLoadingMode = false;
  let isSavingMode = false;

  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="flex items-center justify-between gap-4 flex-wrap pb-1">
        <div>
          <h1 class="page-title">安全控制</h1>
          <p class="page-description">配置全局安全审查策略与管理被锁定的用户。</p>
        </div>
        <button class="btn btn-outline btn-sm" id="security-refresh">
          ${icon('refresh', 'w-3.5 h-3.5')} 刷新
        </button>
      </header>

      <!-- Global Security Control Mode Card -->
      <section class="card p-4">
        <div class="flex items-center justify-between gap-2 border-b border-border pb-3 mb-3">
          <div class="flex items-center gap-2">
            <span class="text-primary flex items-center">${icon('lock', 'w-4 h-4')}</span>
            <h2 class="card-title text-sm font-semibold">全局安全控制模式</h2>
          </div>
          <div class="flex items-center gap-2">
            <span id="security-mode-spinner" class="spinner hidden" style="width: 0.875rem; height: 0.875rem;"></span>
            <span id="security-mode-status" class="badge badge-outline text-xs">加载中…</span>
          </div>
        </div>
        <p class="text-xs text-muted mb-3">
          控制 FrostAgent 对用户输入、上下文、工具调用与回复的语义审查深度。更改将即时同步至所有实例运行时，无需重启服务。
        </p>
        <div id="security-mode-radios" class="grid grid-cols-1 md:grid-cols-3 gap-3">
          ${renderModeOptions(currentMode, true)}
        </div>
      </section>

      <!-- Locked Principals Card -->
      <section class="card table-card overflow-hidden">
        <div class="p-4 border-b border-border">
          <h2 class="text-sm font-semibold">已锁定用户</h2>
          <p class="text-xs text-muted mt-0.5">全局拦截名单，命中后拒绝一切服务交互并在网关层直接阻断。</p>
        </div>
        <div class="table-container">
          <table class="table">
            <thead><tr><th>平台</th><th>用户</th><th>原因</th><th>锁定时间</th><th>操作</th></tr></thead>
            <tbody id="security-locked-body"><tr><td colspan="5">正在加载…</td></tr></tbody>
          </table>
        </div>
      </section>
    </div>`;

  const radioContainer = container.querySelector<HTMLElement>('#security-mode-radios');
  const modeStatusBadge = container.querySelector<HTMLElement>('#security-mode-status');
  const modeSpinner = container.querySelector<HTMLElement>('#security-mode-spinner');
  const lockedTableBody = container.querySelector<HTMLElement>('#security-locked-body');

  function renderModeOptions(selectedMode: SecurityControlMode, disabled: boolean): string {
    return MODE_OPTIONS.map((opt) => {
      const isSelected = opt.value === selectedMode;
      const borderClass = isSelected ? 'border-primary ring-1 ring-primary/40 bg-primary/5' : 'border-border';
      const disabledAttr = disabled ? 'disabled' : '';
      return `
        <label
          class="card p-3.5 flex items-start gap-2.5 cursor-pointer hover-bg transition-all border ${borderClass}"
          data-mode="${opt.value}"
          style="${disabled ? 'opacity: 0.7; pointer-events: none;' : ''}"
        >
          <input
            type="radio"
            name="security-control-mode"
            value="${opt.value}"
            class="radio mt-0.5"
            ${isSelected ? 'checked' : ''}
            ${disabledAttr}
          />
          <div class="flex-1 min-w-0">
            <div class="flex items-center gap-1.5 flex-wrap">
              <span class="text-xs font-semibold text-foreground">${escapeHtml(opt.label)}</span>
              <span class="text-[11px] text-muted font-mono">(${opt.value})</span>
              ${opt.badge ? `<span class="badge badge-primary text-[10px] py-0 px-1.5">${escapeHtml(opt.badge)}</span>` : ''}
            </div>
            <p class="text-[11px] text-muted mt-1 leading-relaxed">${escapeHtml(opt.desc)}</p>
          </div>
        </label>
      `;
    }).join('');
  }

  function updateModeUI(mode: SecurityControlMode, disabled: boolean) {
    if (disposed || !radioContainer) return;
    radioContainer.innerHTML = renderModeOptions(mode, disabled);
    bindRadioEvents();

    if (modeStatusBadge) {
      const opt = MODE_OPTIONS.find((o) => o.value === mode);
      const label = opt ? opt.label : mode;
      modeStatusBadge.textContent = isSavingMode ? '保存中…' : `当前：${label}`;
      modeStatusBadge.className = isSavingMode
        ? 'badge badge-warning text-xs'
        : mode === 'off'
          ? 'badge badge-destructive text-xs'
          : mode === 'aggressive'
            ? 'badge badge-purple text-xs'
            : 'badge badge-primary text-xs';
    }
  }

  function bindRadioEvents() {
    if (!radioContainer) return;
    const inputs = radioContainer.querySelectorAll<HTMLInputElement>('input[name="security-control-mode"]');
    inputs.forEach((input) => {
      input.addEventListener('change', async () => {
        const nextMode = input.value as SecurityControlMode;
        if (nextMode === currentMode || isSavingMode || isLoadingMode) return;
        await handleModeChange(nextMode);
      });
    });
  }

  async function handleModeChange(nextMode: SecurityControlMode) {
    const previousMode = currentMode;
    currentMode = nextMode;
    isSavingMode = true;

    if (modeSpinner) modeSpinner.classList.remove('hidden');
    updateModeUI(nextMode, true);

    try {
      const res = await securityAPI.setMode(nextMode);
      if (disposed) return;
      currentMode = res.mode;
      const opt = MODE_OPTIONS.find((o) => o.value === res.mode);
      toast.success(`全局安全控制模式已切换为：${opt ? opt.label : res.mode}`);
    } catch (error) {
      if (disposed) return;
      // Rollback on failure
      currentMode = previousMode;
      const errMsg = error instanceof Error ? error.message : '未知错误';
      toast.error(`切换安全控制模式失败：${errMsg}`);
    } finally {
      if (!disposed) {
        isSavingMode = false;
        if (modeSpinner) modeSpinner.classList.add('hidden');
        updateModeUI(currentMode, false);
      }
    }
  }

  const renderLockedTable = () => {
    if (!lockedTableBody) return;
    if (records.length === 0) {
      lockedTableBody.innerHTML = '<tr><td colspan="5" class="text-muted">当前没有锁定用户</td></tr>';
      return;
    }
    lockedTableBody.innerHTML = records.map((record, index) => `
      <tr>
        <td>${escapeHtml(record.principal.platform)}</td>
        <td><code>${escapeHtml(record.principal.user_id)}</code></td>
        <td>${escapeHtml(record.reason || '未提供')}</td>
        <td>${escapeHtml(record.locked_at ? formatDateTime(record.locked_at) : '未知')}</td>
        <td><button class="btn btn-outline btn-sm" data-unlock-index="${index}">解除锁定</button></td>
      </tr>`).join('');

    lockedTableBody.querySelectorAll<HTMLButtonElement>('[data-unlock-index]').forEach((button) => {
      button.addEventListener('click', async () => {
        const record = records[Number(button.dataset.unlockIndex)];
        if (!record) return;
        button.disabled = true;
        try {
          await securityAPI.unlockPrincipal(record.principal.platform, record.principal.user_id);
          if (disposed) return;
          toast.success('已解除锁定');
          await loadLocked();
        } catch (error) {
          toast.error(error instanceof Error ? error.message : '解除锁定失败');
          button.disabled = false;
        }
      });
    });
  };

  const loadMode = async () => {
    if (isSavingMode) return;
    isLoadingMode = true;
    if (modeSpinner) modeSpinner.classList.remove('hidden');
    if (modeStatusBadge) {
      modeStatusBadge.textContent = '获取中…';
      modeStatusBadge.className = 'badge badge-outline text-xs';
    }

    try {
      const res = await securityAPI.getMode();
      if (!disposed) {
        currentMode = res.mode;
        updateModeUI(currentMode, false);
      }
    } catch {
      if (!disposed && modeStatusBadge) {
        modeStatusBadge.textContent = '获取模式失败';
        modeStatusBadge.className = 'badge badge-destructive text-xs';
      }
    } finally {
      if (!disposed) {
        isLoadingMode = false;
        if (modeSpinner && !isSavingMode) modeSpinner.classList.add('hidden');
      }
    }
  };

  const loadLocked = async () => {
    try {
      const result = await securityAPI.listLockedPrincipals();
      if (!disposed) {
        records = result.locked || [];
        renderLockedTable();
      }
    } catch (error) {
      if (!disposed && lockedTableBody) {
        lockedTableBody.innerHTML = `<tr><td colspan="5" class="text-error">${escapeHtml(error instanceof Error ? error.message : '加载失败')}</td></tr>`;
      }
    }
  };

  const loadAll = async () => {
    await Promise.allSettled([loadMode(), loadLocked()]);
  };

  container.querySelector('#security-refresh')?.addEventListener('click', () => void loadAll());

  void loadAll();
  return () => {
    disposed = true;
  };
}
