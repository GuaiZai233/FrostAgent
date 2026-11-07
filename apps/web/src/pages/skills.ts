import { createSkillsAPI, type SkillInfo } from '../api/skills';
import { escapeHtml } from '../utils/formatters';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { openDialog } from '../components/dialog';
import { confirmDialog } from '../components/confirm';

export function mountSkillsPage(
  container: HTMLElement,
  api = createSkillsAPI(),
): () => void {
  const lifecycle = new AbortController();
  let skills: SkillInfo[] = [];
  let busy = false;
  let closeDetails: (() => void) | undefined;

  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="flex items-center justify-between gap-4 flex-wrap">
        <h1 class="page-title">Skills</h1>
        <div class="flex items-center gap-2">
          <label class="btn btn-primary btn-sm" style="cursor: pointer;">
            ${icon('upload', 'size-4')}<span>导入 ZIP</span>
            <input id="skills-upload" type="file" accept=".zip,application/zip" hidden>
          </label>
          <button id="skills-refresh" class="btn btn-outline btn-icon-sm" aria-label="刷新">${icon('refresh', 'size-4')}</button>
        </div>
      </header>
      <div id="skills-list" class="flex flex-col gap-3" aria-live="polite">加载中...</div>
    </div>`;
  const list = container.querySelector<HTMLElement>('#skills-list')!;
  const upload = container.querySelector<HTMLInputElement>('#skills-upload')!;
  const refresh =
    container.querySelector<HTMLButtonElement>('#skills-refresh')!;

  function render() {
    if (lifecycle.signal.aborted) return;
    upload.disabled = busy;
    refresh.disabled = busy;
    list.innerHTML = skills.length
      ? skills
          .map(
            (skill) => `
      <article class="card p-4 flex flex-col gap-3">
        <div class="flex items-center justify-between gap-3 flex-wrap">
          <div class="flex items-center gap-2 flex-wrap">
            <h2 class="font-semibold">${escapeHtml(skill.name)}</h2>
            <span class="badge badge-outline">${skill.enabled ? '已启用' : '已禁用'}</span>
            ${skill.manual_only ? '<span class="badge badge-outline">仅手动调用</span>' : ''}
          </div>
          <div class="flex items-center gap-2">
            <button class="btn btn-outline btn-sm" data-action="view" data-name="${escapeHtml(skill.name)}" ${busy ? 'disabled' : ''}>查看</button>
            <button class="btn btn-outline btn-sm" data-action="toggle" data-name="${escapeHtml(skill.name)}" ${busy ? 'disabled' : ''}>${skill.enabled ? '禁用' : '启用'}</button>
            <button class="btn btn-ghost btn-sm" data-action="delete" data-name="${escapeHtml(skill.name)}" ${busy ? 'disabled' : ''}>删除</button>
          </div>
        </div>
        <p class="text-sm text-muted break-words">${escapeHtml(skill.description)}</p>
        ${skill.compatibility ? `<p class="text-xs text-muted break-words">环境要求：${escapeHtml(skill.compatibility)}</p>` : ''}
        ${skill.warnings.map((warning) => `<p class="text-xs text-muted break-words">${escapeHtml(warning)}</p>`).join('')}
      </article>`,
          )
          .join('')
      : '<div class="card p-6 text-sm text-muted">暂无技能，上传包含 SKILL.md 的 ZIP 即可导入。</div>';
  }

  function report(error: unknown) {
    if (!lifecycle.signal.aborted)
      toast.error(error instanceof Error ? error.message : String(error));
  }

  async function reload() {
    skills = await api.list();
    render();
  }

  async function perform(action: () => Promise<void>) {
    if (busy || lifecycle.signal.aborted) return;
    busy = true;
    render();
    try {
      await action();
    } catch (error) {
      report(error);
    } finally {
      busy = false;
      render();
    }
  }

  refresh.addEventListener(
    'click',
    () => {
      void perform(reload);
    },
    { signal: lifecycle.signal },
  );
  upload.addEventListener(
    'change',
    () => {
      const file = upload.files?.[0];
      if (!file) return;
      void perform(async () => {
        const imported = await api.importZIP(file);
        if (lifecycle.signal.aborted) return;
        toast.success(`已导入 ${imported.length} 个技能，默认禁用`);
        upload.value = '';
        await reload();
      });
    },
    { signal: lifecycle.signal },
  );

  list.addEventListener(
    'click',
    (event) => {
      const button = (event.target as Element).closest<HTMLButtonElement>(
        'button[data-action]',
      );
      if (!button || button.disabled || busy) return;
      const name = button.dataset.name!;
      const skill = skills.find((item) => item.name === name);
      if (!skill) return;
      void perform(async () => {
        if (button.dataset.action === 'delete') {
          const confirmed = await confirmDialog({
            title: '删除技能',
            message: `删除 ${name} 及全部资源？`,
            destructive: true,
            signal: lifecycle.signal,
          });
          if (!confirmed || lifecycle.signal.aborted) return;
          await api.delete(name);
          await reload();
        } else if (button.dataset.action === 'toggle') {
          await api.setEnabled(name, !skill.enabled);
          await reload();
        } else {
          const details = await api.get(name);
          if (lifecycle.signal.aborted) return;
          void openDialog({
            title: name,
            bodyHtml: `<pre class="text-xs whitespace-pre-wrap break-words select-text">${escapeHtml(details.source)}</pre><hr class="my-4"><pre class="text-xs whitespace-pre-wrap break-words">${escapeHtml(details.info.files.join('\n'))}</pre>`,
            maxWidth: '48rem',
            onMount: (_dialog, close) => {
              closeDetails = close;
            },
            onClose: () => {
              closeDetails = undefined;
            },
          });
        }
      });
    },
    { signal: lifecycle.signal },
  );

  void perform(reload);
  return () => {
    lifecycle.abort();
    closeDetails?.();
  };
}
