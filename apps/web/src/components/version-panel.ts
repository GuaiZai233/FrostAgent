import { fetchBuildVersion, fetchUpdateCheck, type BuildVersion, type UpdateChannel } from '../api/update-check';
import { escapeHtml } from '../utils/formatters';
import { icon } from './icons';

export function mountVersionPanel(container: HTMLElement): () => void {
  const lifecycle = new AbortController();
  let checking = false;
  let versionRequest = 0;
  container.innerHTML = `
    <section class="card p-4 flex flex-col gap-3" aria-label="版本与更新">
      <div class="flex items-center justify-between gap-3 flex-wrap">
        <div class="min-w-0">
          <h2 class="text-sm font-semibold">FrostAgent 版本</h2>
          <p id="build-version" class="text-xs text-muted mt-1 break-all" role="status">正在读取版本…</p>
        </div>
        <button type="button" class="btn btn-outline btn-sm" id="check-updates">${icon('refresh', 'w-3.5 h-3.5')}检查更新</button>
      </div>
      <p class="text-xs text-muted">仅查询 GitHub 仓库：Stable 为最新正式 Release tag，Latest 为默认分支最新提交。不会自动升级；检查结果缓存 1 分钟。</p>
      <div id="update-results" class="grid grid-cols-1 sm:grid-cols-2 gap-3" aria-live="polite"></div>
      <p id="update-checked-at" class="text-xs text-muted"></p>
    </section>`;
  const version = container.querySelector<HTMLElement>('#build-version')!;
  const button = container.querySelector<HTMLButtonElement>('#check-updates')!;
  const results = container.querySelector<HTMLElement>('#update-results')!;
  const checkedAt = container.querySelector<HTMLElement>('#update-checked-at')!;

  function showVersion(build: BuildVersion) {
    version.textContent = `当前：${build.display || 'unknown'}${build.commit ? ` · ${build.commit}` : ''}`;
  }

  const initialRequest = ++versionRequest;
  void fetchBuildVersion(lifecycle.signal).then((build) => {
    if (!lifecycle.signal.aborted && initialRequest === versionRequest) showVersion(build);
  }).catch((error) => {
    if (!lifecycle.signal.aborted && initialRequest === versionRequest) version.textContent = String(error);
  });

  button.addEventListener('click', async () => {
    if (checking || lifecycle.signal.aborted) return;
    checking = true;
    button.disabled = true;
    results.textContent = '正在检查 GitHub…';
    checkedAt.textContent = '';
    try {
      const result = await fetchUpdateCheck(lifecycle.signal);
      if (lifecycle.signal.aborted) return;
      versionRequest++;
      showVersion(result.current);
      results.innerHTML = renderChannel('Stable', result.stable) + renderChannel('Latest', result.latest);
      checkedAt.textContent = `检查时间：${new Date(result.checked_at).toLocaleString()}`;
    } catch (error) {
      if (!lifecycle.signal.aborted) results.textContent = String(error);
    } finally {
      checking = false;
      if (!lifecycle.signal.aborted) button.disabled = false;
    }
  });
  return () => lifecycle.abort();
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
