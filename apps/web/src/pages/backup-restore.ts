import { instanceState, instanceRequest } from '../instance-state';
import { icon } from '../components/icons';
import { toast } from '../components/toast';

export function mountBackupRestorePage(container: HTMLElement): () => void {
  const id = instanceState.selected!.id;
  const base = `/api/instances/${id}/backup`;
  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="pb-1">
        <a href="#/settings" class="btn btn-ghost btn-icon-sm" title="返回设置">${icon('arrow_left', 'w-4 h-4')}</a>
        <h1 class="page-title">备份与还原</h1>
        <p class="page-description">当前实例的备份会直接下载到浏览器。密钥不会包含在导出文件中。</p>
      </header>
      <article class="card p-5 mt-4">
        <h2 class="card-title text-base">一键全量备份</h2>
        <p class="text-sm text-muted mt-1">包含设置、私聊及群聊记忆、档案、摘要和贴图。贴图图片作为 ZIP 中的独立文件保存。</p>
        <a href="${base}" download="frostagent-${id}.zip" class="btn btn-primary mt-4">下载一键全量备份 ZIP</a>
      </article>
      <article class="card p-5 mt-4">
        <h2 class="card-title text-base">快速备份</h2>
        <div class="flex flex-wrap gap-2 mt-4">
          <a href="${base}/settings" download="setting.json" class="btn btn-outline">设置 JSON</a>
          <a href="${base}/memories" download="memory.json" class="btn btn-outline">记忆 JSON</a>
          <a href="${base}/stickers" download="stickers.zip" class="btn btn-outline">贴图 ZIP</a>
          <a href="${base}/summaries" download="group_summaries.json" class="btn btn-outline">摘要 JSON</a>
        </div>
      </article>
      <article class="card p-5 mt-4">
        <h2 class="card-title text-base">全局设置</h2>
        <p class="text-sm text-muted mt-1">全局设置单独备份；密钥与凭据来源置空，导入时保留当前密钥。</p>
        <a href="/api/instances/global/backup/settings" download="global-setting.json" class="btn btn-outline mt-4">下载全局设置 JSON</a>
        <input id="global-import-file" class="input mt-4" type="file" accept=".json,application/json" />
        <button id="global-import-button" class="btn btn-outline mt-3" disabled>导入全局设置</button>
      </article>
      <article class="card p-5 mt-4">
        <h2 class="card-title text-base">快速导入设置</h2>
        <p class="text-sm text-muted mt-1">选择 setting.json 后，将一次性替换当前实例的非密钥设置、模型路由、MCP 和示例对话。导入文件中的密钥会被忽略。</p>
        <input id="settings-import-file" class="input mt-4" type="file" accept=".json,application/json" />
        <button id="settings-import-button" class="btn btn-outline mt-3" disabled>导入 setting.json</button>
      </article>
      <article class="card p-5 mt-4">
        <h2 class="card-title text-base">快速导入记忆</h2>
        <p class="text-sm text-muted mt-1">按 ID 合并记忆和归档，同 ID 跳过，现有群档案优先。不同 ID 的相同内容可能重复，导入后请到记忆管理页面人工查重。</p>
        <input id="memory-import-file" class="input mt-4" type="file" accept=".json,application/json" />
        <button id="memory-import-button" class="btn btn-outline mt-3" disabled>合并导入 memory.json</button>
      </article>
      <article class="card p-5 mt-4">
        <h2 class="card-title text-base">全量还原为新实例</h2>
        <p class="text-sm text-muted mt-1">选择 FrostAgent 全量 ZIP 后创建一个全新实例。还原后的实例默认停用，重新配置密钥和适配器后再启用。</p>
        <input id="restore-instance-name" class="input mt-4" type="text" maxlength="32" placeholder="新实例名称（留空自动命名）" />
        <input id="restore-instance-file" class="input mt-3" type="file" accept=".zip,application/zip" />
        <button id="restore-instance-button" class="btn btn-outline mt-3" disabled>还原为新实例</button>
      </article>
    </div>`;
  const fileInput = container.querySelector<HTMLInputElement>('#settings-import-file')!;
  const globalFile = container.querySelector<HTMLInputElement>('#global-import-file')!;
  const globalButton = container.querySelector<HTMLButtonElement>('#global-import-button')!;
  globalFile.onchange = () => { globalButton.disabled = !globalFile.files?.length; };
  globalButton.onclick = () => {
    void (async () => {
      const file = globalFile.files?.[0];
      if (!file) return;
      globalButton.disabled = true;
      try {
        await instanceRequest('/global/import/settings', JSON.parse(await file.text()));
        toast.success('全局设置已导入，正在自动应用');
      } catch (error) {
        toast.error('导入全局设置失败: ' + String(error));
      } finally {
        globalButton.disabled = false;
      }
    })();
  };
  const importButton = container.querySelector<HTMLButtonElement>('#settings-import-button')!;
  fileInput.onchange = () => { importButton.disabled = !fileInput.files?.length; };
  importButton.onclick = () => {
    void (async () => {
      const file = fileInput.files?.[0];
      if (!file) return;
      importButton.disabled = true;
      try {
        const contents = JSON.parse(await file.text());
        await instanceRequest(`/${id}/import/settings`, contents);
        toast.success('设置已导入；请重新配置密钥');
      } catch (error) {
        toast.error('导入设置失败: ' + String(error));
      } finally {
        importButton.disabled = false;
      }
    })();
  };
  const memoryFile = container.querySelector<HTMLInputElement>('#memory-import-file')!;
  const memoryButton = container.querySelector<HTMLButtonElement>('#memory-import-button')!;
  memoryFile.onchange = () => { memoryButton.disabled = !memoryFile.files?.length; };
  memoryButton.onclick = () => {
    void (async () => {
      const file = memoryFile.files?.[0];
      if (!file) return;
      memoryButton.disabled = true;
      try {
        const contents = JSON.parse(await file.text());
        const result = await instanceRequest<{ imported: number; skipped: number }>(`/${id}/import/memories`, contents);
        toast.success(`导入 ${result.imported} 条，跳过 ${result.skipped} 条；请检查不同 ID 的重复内容`);
      } catch (error) {
        toast.error('导入记忆失败: ' + String(error));
      } finally {
        memoryButton.disabled = false;
      }
    })();
  };
  const restoreFile = container.querySelector<HTMLInputElement>('#restore-instance-file')!;
  const restoreName = container.querySelector<HTMLInputElement>('#restore-instance-name')!;
  const restoreButton = container.querySelector<HTMLButtonElement>('#restore-instance-button')!;
  restoreFile.onchange = () => { restoreButton.disabled = !restoreFile.files?.length; };
  restoreButton.onclick = () => {
    void (async () => {
      const file = restoreFile.files?.[0];
      if (!file) return;
      restoreButton.disabled = true;
      try {
        const name = restoreName.value.trim();
        const response = await fetch(`/api/instances/restore?name=${encodeURIComponent(name)}`, {
          method: 'POST', body: file,
        });
        const result = await response.json();
        if (!response.ok) throw new Error(result.error || response.statusText);
        await instanceState.refresh();
        instanceState.select(result);
        toast.success('已创建停用的新实例；请重新配置密钥和适配器');
      } catch (error) {
        toast.error('全量还原失败: ' + String(error));
      } finally {
        restoreButton.disabled = false;
      }
    })();
  };
  return () => {};
}
