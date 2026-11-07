import { fetchBuildVersion, fetchUpdateCheck, type BuildVersion, type UpdateChannel } from '../api/update-check';
import { escapeHtml } from '../utils/formatters';
import { icon } from './icons';
import { openDialog } from './dialog';

export function mountVersionControl(container: HTMLElement): () => void {
  const lifecycle = new AbortController();
  let versionRequest = 0;
  let closeDialog: (() => void) | undefined;
  container.innerHTML = `
    <div class="flex items-center gap-1.5" aria-label="版本与更新">
      <span class="badge badge-outline">
        ${icon('code', 'w-3 h-3')}
        <span id="build-version" role="status">…</span>
      </span>
      <button type="button" class="btn btn-outline btn-sm" id="check-updates" aria-haspopup="dialog">${icon('square_pen', 'w-3.5 h-3.5')}检查更新</button>
    </div>`;
  const version = container.querySelector<HTMLElement>('#build-version')!;
  const button = container.querySelector<HTMLButtonElement>('#check-updates')!;

  function showVersion(build: BuildVersion) {
    version.textContent = build.display || 'unknown';
    version.title = build.commit || build.display || 'unknown';
  }

  const initialRequest = ++versionRequest;
  void fetchBuildVersion(lifecycle.signal).then((build) => {
    if (!lifecycle.signal.aborted && initialRequest === versionRequest) showVersion(build);
  }).catch((error) => {
    if (!lifecycle.signal.aborted && initialRequest === versionRequest) {
      version.textContent = 'unknown';
      version.title = String(error);
    }
  });

  button.addEventListener('click', () => {
    if (closeDialog || lifecycle.signal.aborted) return;
    const request = new AbortController();
    void openDialog({
      title: '检查更新',
      maxWidth: '40rem',
      bodyHtml: `<div class="flex flex-col gap-3">
        <p id="update-current-version" class="text-xs text-muted break-all">当前：${escapeHtml(version.textContent || 'unknown')}</p>
        <div id="update-results" class="grid grid-cols-1 sm:grid-cols-2 gap-3" aria-live="polite">正在检查 GitHub…</div>
        <p id="update-checked-at" class="text-xs text-muted"></p>
      </div>`,
      onMount(dialog, close) {
        closeDialog = close;
        const results = dialog.querySelector<HTMLElement>('#update-results')!;
        void fetchUpdateCheck(request.signal).then((result) => {
          if (request.signal.aborted) return;
          versionRequest++;
          showVersion(result.current);
          dialog.querySelector<HTMLElement>('#update-current-version')!.textContent = `当前：${result.current.display || 'unknown'}${result.current.commit ? ` · ${result.current.commit}` : ''}`;
          results.innerHTML = renderChannel('Stable', result.stable) + renderChannel('Latest', result.latest);
          dialog.querySelector<HTMLElement>('#update-checked-at')!.textContent = `检查时间：${new Date(result.checked_at).toLocaleString()}`;
        }).catch((error) => {
          if (!request.signal.aborted) results.textContent = String(error);
        });
      },
      onClose() {
        request.abort();
        closeDialog = undefined;
      },
    });
  });
  return () => {
    lifecycle.abort();
    closeDialog?.();
  };
}

function renderChannel(name: 'Stable' | 'Latest', channel: UpdateChannel): string {
  const labels: Record<UpdateChannel['status'], string> = {
    'up-to-date': '与目标一致',
    different: '与目标不同，请查看 GitHub 变更后决定是否更新',
    unknown: '当前构建缺少此渠道的版本信息，无法自动比较',
    unavailable: '检查失败',
    'no-release': '暂无正式 Release',
  };
  // Construct links locally: never render a URL supplied by an upstream response.
  const value = channel.value || '';
  const url = name === 'Stable'
    ? `https://github.com/GuaiZai233/FrostAgent/releases/tag/${encodeURIComponent(value)}`
    : `https://github.com/GuaiZai233/FrostAgent/commit/${encodeURIComponent(value)}`;
  return `<article class="rounded-md border p-3 min-w-0">
    <h3 class="text-sm font-semibold">${name}</h3>
    ${value ? `<a class="text-xs font-mono break-all" href="${escapeHtml(url)}" target="_blank" rel="noopener noreferrer">${escapeHtml(value)}</a>` : ''}
    <p class="text-xs text-muted mt-1">${escapeHtml(channel.error || labels[channel.status] || '未知状态')}</p>
  </article>`;
}
