import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { instanceState } from '../instance-state';

interface StorageConfig {
  backend: 'sqlite' | 'postgres';
  dsn: string;
}

export function mountStorageSettingsPage(container: HTMLElement): () => void {
  let active = true;
  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="pb-1">
        <a href="#/settings" class="btn btn-ghost btn-icon-sm" title="返回设置">${icon('arrow_left', 'w-4 h-4')}</a>
        <h1 class="page-title">数据库设置</h1>
        <p class="page-description">选择本地 SQLite 或 PostgreSQL。切换后会立即连接目标数据库并加载其中已有的数据。</p>
      </header>
      <article class="card p-5 mt-4" style="max-width: 42rem;">
        <div class="form-group">
          <label class="form-label" for="storage-backend">数据库类型</label>
          <select class="select" id="storage-backend">
            <option value="sqlite">本地 SQLite</option>
            <option value="postgres">PostgreSQL</option>
          </select>
        </div>
        <div class="form-group mt-4" id="storage-dsn-group">
          <label class="form-label" for="storage-dsn">PostgreSQL 连接地址</label>
          <input class="input font-mono" id="storage-dsn" type="text" autocomplete="off" placeholder="postgres://user:password@host:5432/database" />
          <p class="form-hint">选择 PostgreSQL 时必须填写。地址无效或连接失败时维持当前数据库。</p>
        </div>
        <p class="text-sm text-muted mt-4">数据库切换不会迁移数据。切换前可在“备份与还原”导出实例存档，再在目标库手动还原。</p>
        <button class="btn btn-primary mt-4" id="storage-save" disabled>保存并切换</button>
      </article>
    </div>
  `;
  const backend = container.querySelector<HTMLSelectElement>('#storage-backend')!;
  const dsn = container.querySelector<HTMLInputElement>('#storage-dsn')!;
  const dsnGroup = container.querySelector<HTMLElement>('#storage-dsn-group')!;
  const save = container.querySelector<HTMLButtonElement>('#storage-save')!;
  const updateVisibility = () => {
    dsnGroup.hidden = backend.value !== 'postgres';
  };
  backend.onchange = updateVisibility;
  fetch('/api/storage')
    .then(async response => {
      const result = await response.json();
      if (!response.ok) throw new Error(result.error || response.statusText);
      return result as StorageConfig;
    })
    .then(config => {
      if (!active) return;
      backend.value = config.backend;
      dsn.value = config.dsn || '';
      updateVisibility();
      save.disabled = false;
    })
    .catch(error => { if (active) toast.error('加载数据库设置失败: ' + String(error)); });
  save.onclick = async () => {
    if (backend.value === 'postgres' && !dsn.value.trim()) {
      toast.error('请先填写 PostgreSQL 连接地址');
      return;
    }
    save.disabled = true;
    try {
      const response = await fetch('/api/storage', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ backend: backend.value, dsn: backend.value === 'postgres' ? dsn.value.trim() : '' }),
      });
      const result = await response.json();
      if (!response.ok) throw new Error(result.error || response.statusText);
      instanceState.select(null);
      toast.success('数据库已切换，并已加载目标库现有配置');
      try {
        await instanceState.refresh();
      } catch {
        const address = typeof result.listen_addr === 'string' && result.listen_addr.trim()
          ? result.listen_addr.trim()
          : '目标数据库配置的管理地址';
        toast.error(`数据库已切换，但当前页面无法连接新监听地址。请从 ${address} 重新打开管理页面。`);
      }
    } catch (error) {
      toast.error('数据库切换失败: ' + String(error));
    } finally {
      if (active) save.disabled = false;
    }
  };
  return () => { active = false; };
}
