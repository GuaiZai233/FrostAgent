import { securityAPI, type LockedPrincipal } from '../api/client';
import { escapeHtml, formatDateTime } from '../utils/formatters';
import { toast } from '../components/toast';
import { icon } from '../components/icons';

export function mountSecurityPage(container: HTMLElement): () => void {
  let disposed = false;
  let records: LockedPrincipal[] = [];

  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="flex items-center justify-between gap-4 flex-wrap pb-1">
        <div>
          <h1 class="page-title">安全控制</h1>
          <p class="page-description">查看全局锁定用户，并在确认后恢复访问。</p>
        </div>
        <button class="btn btn-outline btn-sm" id="security-refresh">${icon('refresh', 'w-3.5 h-3.5')} 刷新</button>
      </header>
      <section class="card table-card overflow-hidden">
        <div class="table-container">
          <table class="table">
            <thead><tr><th>平台</th><th>用户</th><th>原因</th><th>锁定时间</th><th>操作</th></tr></thead>
            <tbody id="security-locked-body"><tr><td colspan="5">正在加载…</td></tr></tbody>
          </table>
        </div>
      </section>
    </div>`;

  const body = container.querySelector<HTMLElement>('#security-locked-body');
  const render = () => {
    if (!body) return;
    if (records.length === 0) {
      body.innerHTML = '<tr><td colspan="5" class="text-muted">当前没有锁定用户</td></tr>';
      return;
    }
    body.innerHTML = records.map((record, index) => `
      <tr>
        <td>${escapeHtml(record.principal.platform)}</td>
        <td><code>${escapeHtml(record.principal.user_id)}</code></td>
        <td>${escapeHtml(record.reason || '未提供')}</td>
        <td>${escapeHtml(record.locked_at ? formatDateTime(record.locked_at) : '未知')}</td>
        <td><button class="btn btn-outline btn-sm" data-unlock-index="${index}">解除锁定</button></td>
      </tr>`).join('');
    body.querySelectorAll<HTMLButtonElement>('[data-unlock-index]').forEach((button) => {
      button.addEventListener('click', async () => {
        const record = records[Number(button.dataset.unlockIndex)];
        if (!record) return;
        button.disabled = true;
        try {
          await securityAPI.unlockPrincipal(record.principal.platform, record.principal.user_id);
          if (disposed) return;
          toast.success('已解除锁定');
          await load();
        } catch (error) {
          toast.error(error instanceof Error ? error.message : '解除锁定失败');
          button.disabled = false;
        }
      });
    });
  };

  const load = async () => {
    try {
      const result = await securityAPI.listLockedPrincipals();
      if (!disposed) {
        records = result.locked || [];
        render();
      }
    } catch (error) {
      if (!disposed && body) body.innerHTML = `<tr><td colspan="5" class="text-error">${escapeHtml(error instanceof Error ? error.message : '加载失败')}</td></tr>`;
    }
  };

  container.querySelector('#security-refresh')?.addEventListener('click', () => void load());
  void load();
  return () => { disposed = true; };
}
