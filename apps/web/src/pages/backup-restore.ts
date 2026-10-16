import { instanceState, instanceRequest, restoreInstanceZIP } from '../instance-state';
import { globalSettingsAPI } from '../api/client';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { escapeHtml } from '../utils/formatters';

export function mountBackupRestorePage(container: HTMLElement): () => void {
  const id = instanceState.selected!.id;
  const base = `/api/instances/${id}/backup`;
  container.innerHTML = `
    <div class="page-container backup-page fade-in">
      <header class="backup-page-header">
        <a href="#/settings" class="btn btn-ghost btn-icon-sm" title="返回设置">${icon('arrow_left', 'w-4 h-4')}</a>
        <div class="min-w-0">
          <h1 class="page-title">备份与还原</h1>
          <p class="text-sm text-muted mt-1">为实例保存一份存档，或从已有备份继续。</p>
        </div>
        <span class="badge badge-outline backup-instance">${icon('bot', 'w-3.5 h-3.5')} ${escapeHtml(instanceState.selected!.name)}</span>
      </header>

      <article class="card backup-feature">
        <div class="backup-feature-copy">
          <div class="backup-icon">${icon('download', 'w-5 h-5')}</div>
          <div class="min-w-0">
            <h2 class="text-base font-semibold">一键全量备份</h2>
            <p class="text-sm text-muted mt-1">一次保存设置、记忆、档案、摘要和贴图，图片作为 ZIP 内的独立文件保留。</p>
          </div>
        </div>
        <a href="${base}" download="frostagent-${id}.zip" class="btn btn-primary">${icon('download', 'w-4 h-4')} 下载全量 ZIP</a>
      </article>
      <p class="backup-note">${icon('info', 'w-4 h-4')}<span>备份直接下载到浏览器，密钥与凭据来源置空；只包含当前实例引用的共享供应商与模型。</span></p>

      <section class="backup-section" aria-labelledby="backup-parts-title">
        <div class="backup-section-heading"><h2 id="backup-parts-title">分项备份</h2><span>只带走需要的部分</span></div>
        <div class="backup-download-grid">
          ${[
            { path: 'settings', file: 'setting.json', title: '实例设置', detail: '模型路由、MCP 与示例对话', format: 'JSON', symbol: 'sliders' },
            { path: 'memories', file: 'memory.json', title: '记忆与档案', detail: '私聊及群聊记忆、归档', format: 'JSON', symbol: 'brain' },
            { path: 'stickers', file: 'stickers.zip', title: '贴图', detail: '贴图信息与原始图片', format: 'ZIP', symbol: 'image' },
            { path: 'summaries', file: 'group_summaries.json', title: '群摘要', detail: '群聊的运行摘要', format: 'JSON', symbol: 'file_text' },
          ].map((part) => `
            <a href="${base}/${part.path}" download="${part.file}" class="card backup-download">
              <div class="backup-download-top">${icon(part.symbol, 'w-5 h-5')}<span class="badge badge-outline">${part.format}</span></div>
              <div><h3 class="text-sm font-semibold">${part.title}</h3><p class="text-xs text-muted mt-1">${part.detail}</p></div>
              <span class="backup-download-action">下载 ${part.format} ${icon('arrow_down', 'w-3.5 h-3.5')}</span>
            </a>`).join('')}
        </div>
      </section>

      <section class="backup-section" aria-labelledby="backup-import-title">
        <div class="backup-section-heading"><h2 id="backup-import-title">导入与还原</h2><span>选择备份文件后执行</span></div>
        <div class="backup-import-grid">
          <article class="card backup-panel">
            <div class="backup-panel-heading">${icon('upload', 'w-4 h-4')}<h3>全量还原为新实例</h3></div>
            <p class="backup-description">从全量 ZIP 创建一个新实例。新实例默认停用，检查供应商凭据与适配器后再启用。</p>
            <div class="backup-form">
              <label for="restore-instance-file">全量备份 ZIP</label>
              <input id="restore-instance-file" class="input" type="file" accept=".zip,application/zip" />
              <button id="restore-instance-button" class="btn btn-outline" disabled>还原为新实例</button>
            </div>
          </article>
          <article class="card backup-panel">
            <div class="backup-panel-heading">${icon('sliders', 'w-4 h-4')}<h3>导入实例设置</h3></div>
            <p class="backup-description">替换当前实例的非密钥设置、模型路由、MCP 和示例对话。文件中的密钥会被忽略。</p>
            <details class="backup-details"><summary>${icon('chevron_right', 'w-3.5 h-3.5')}共享供应商如何处理</summary><p>匹配的供应商复用已有凭据；定义冲突时新增供应商并置空密钥，其他实例使用的配置保持不变。</p></details>
            <div class="backup-form">
              <label for="settings-import-file">设置文件 setting.json</label>
              <input id="settings-import-file" class="input" type="file" accept=".json,application/json" />
              <button id="settings-import-button" class="btn btn-outline" disabled>导入设置</button>
            </div>
          </article>
          <article class="card backup-panel">
            <div class="backup-panel-heading">${icon('brain', 'w-4 h-4')}<h3>合并导入记忆</h3></div>
            <p class="backup-description">按 ID 合并记忆和归档，同 ID 跳过，现有群档案优先。不同 ID 可能包含重复内容，请在记忆管理中人工查重。</p>
            <div class="backup-form">
              <label for="memory-import-file">记忆文件 memory.json</label>
              <input id="memory-import-file" class="input" type="file" accept=".json,application/json" />
              <button id="memory-import-button" class="btn btn-outline" disabled>合并导入记忆</button>
            </div>
          </article>
        </div>
      </section>

      <section class="backup-section" aria-labelledby="backup-global-title">
        <div class="backup-section-heading"><h2 id="backup-global-title">全局数据</h2><span>作用于所有实例，单独备份与导入</span></div>
        <div class="backup-global-grid">
          <article class="card backup-panel">
            <div class="backup-panel-heading">${icon('settings', 'w-4 h-4')}<h3>全局设置</h3></div>
            <p class="backup-description">常规设置的密钥与凭据来源会置空；额外 env 注入项会以明文包含在文件中。请妥善保管，导入后重启 FrostAgent 才会应用注入项。</p>
            <button id="global-download-button" type="button" class="btn btn-outline">${icon('download', 'w-4 h-4')} 下载全局设置 JSON</button>
            <div class="backup-form backup-form-separated">
              <label for="global-import-file">全局设置文件</label>
              <input id="global-import-file" class="input" type="file" accept=".json,application/json" />
              <button id="global-import-button" class="btn btn-outline" disabled>导入全局设置</button>
            </div>
          </article>
          <article class="card backup-panel">
            <div class="backup-panel-heading">${icon('lock', 'w-4 h-4')}<h3>全局访问锁与审计</h3></div>
            <p class="backup-description">包含全局访问状态和安全审计记录。导入会替换当前记录。</p>
            <a href="/api/instances/global/backup/security" download="global-security.json" class="btn btn-outline">${icon('download', 'w-4 h-4')} 下载访问与审计 JSON</a>
            <div class="backup-form backup-form-separated">
              <label for="security-import-file">访问与审计文件</label>
              <input id="security-import-file" class="input" type="file" accept=".json,application/json" />
              <button id="security-import-button" class="btn btn-outline" disabled>导入访问与审计</button>
            </div>
          </article>
        </div>
      </section>
    </div>`;
  const fileInput = container.querySelector<HTMLInputElement>('#settings-import-file')!;
  const globalDownloadButton = container.querySelector<HTMLButtonElement>('#global-download-button')!;
  globalDownloadButton.onclick = () => {
    void (async () => {
      globalDownloadButton.disabled = true;
      try {
        const blob = await globalSettingsAPI.download();
        const url = URL.createObjectURL(blob);
        const link = document.createElement('a');
        link.href = url;
        link.download = 'global-setting.json';
        document.body.appendChild(link);
        link.click();
        link.remove();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
      } catch (error) {
        toast.error('下载全局设置失败: ' + String(error));
      } finally {
        globalDownloadButton.disabled = false;
      }
    })();
  };
  const globalFile = container.querySelector<HTMLInputElement>('#global-import-file')!;
  const globalButton = container.querySelector<HTMLButtonElement>('#global-import-button')!;
  globalFile.onchange = () => { globalButton.disabled = !globalFile.files?.length; };
  globalButton.onclick = () => {
    void (async () => {
      const file = globalFile.files?.[0];
      if (!file) return;
      globalButton.disabled = true;
      try {
        await globalSettingsAPI.import(JSON.parse(await file.text()));
        toast.success('全局设置已导入；额外 env 注入项重启 FrostAgent 后生效');
      } catch (error) {
        toast.error('导入全局设置失败: ' + String(error));
      } finally {
        globalButton.disabled = false;
      }
    })();
  };
  const securityFile = container.querySelector<HTMLInputElement>('#security-import-file')!;
  const securityButton = container.querySelector<HTMLButtonElement>('#security-import-button')!;
  securityFile.onchange = () => { securityButton.disabled = !securityFile.files?.length; };
  securityButton.onclick = () => {
    void (async () => {
      const file = securityFile.files?.[0];
      if (!file) return;
      securityButton.disabled = true;
      try {
        await instanceRequest('/global/import/security', JSON.parse(await file.text()));
        toast.success('全局访问与审计记录已导入');
      } catch (error) {
        toast.error('导入全局访问与审计失败: ' + String(error));
      } finally {
        securityButton.disabled = false;
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
        toast.success('设置已导入；请检查新增供应商的密钥');
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
  const restoreButton = container.querySelector<HTMLButtonElement>('#restore-instance-button')!;
  restoreFile.onchange = () => { restoreButton.disabled = !restoreFile.files?.length; };
  restoreButton.onclick = () => {
    void (async () => {
      const file = restoreFile.files?.[0];
      if (!file) return;
      restoreButton.disabled = true;
      try {
        const result = await restoreInstanceZIP(file);
        await instanceState.refresh();
        instanceState.select(result);
        toast.success('已创建停用的新实例；请检查供应商凭据和适配器');
      } catch (error) {
        toast.error('全量还原失败: ' + String(error));
      } finally {
        restoreButton.disabled = false;
      }
    })();
  };
  return () => {};
}
