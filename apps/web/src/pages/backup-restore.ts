import { instanceState } from '../instance-state';
import { icon } from '../components/icons';

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
    </div>`;
  return () => {};
}
