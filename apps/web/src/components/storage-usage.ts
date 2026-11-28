import type { StorageUsageOverview } from '@frostagent/proto';
import { escapeHtml } from '../utils/formatters';

const categoryLabels: Record<string, string> = {
  chat: '聊天记录',
  images: '图片',
  stickers: '表情包',
  memory: '记忆',
  other: '其他（日志、工具记录等）',
};

export function formatBytes(value: bigint | number): string {
  let size = Number(value);
  if (!Number.isFinite(size) || size <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${unit === 0 ? size : size.toFixed(size >= 100 ? 0 : 1)} ${units[unit]}`;
}

function percent(part: bigint, total: bigint): string {
  if (total <= 0n) return '0.0%';
  const value = Number((part * 1000n) / total) / 10;
  return `${value.toFixed(1)}%`;
}

/** Stacked bar plus a text legend; segments never rely on color alone. */
export function renderStorageUsage(usage: StorageUsageOverview | undefined): string {
  if (!usage) return '';
  if (!usage.available) {
    return `<section class="flex flex-col gap-3" aria-labelledby="storage-usage-title">
      <h2 id="storage-usage-title" class="text-sm font-semibold text-foreground">存储空间占用</h2>
      <div class="card p-4 text-sm text-muted">暂时无法读取存储占用统计</div>
    </section>`;
  }
  const total = usage.totalBytes;
  const items = usage.items.map((item) => ({
    key: item.category,
    label: categoryLabels[item.category] ?? item.category,
    bytes: item.bytes,
  }));
  const summary = items.map((item) => `${item.label} ${formatBytes(item.bytes)}`).join('，');
  const segments = total > 0n
    ? items.filter((item) => item.bytes > 0n).map((item) =>
      `<span class="storage-usage-segment" data-storage-category="${escapeHtml(item.key)}" style="flex-grow:${item.bytes}"></span>`).join('')
    : '';
  return `<section class="flex flex-col gap-3" aria-labelledby="storage-usage-title">
    <div class="flex items-baseline justify-between gap-3 flex-wrap">
      <h2 id="storage-usage-title" class="text-sm font-semibold text-foreground">存储空间占用</h2>
      <p class="text-xs text-muted" data-storage-total>合计 ${escapeHtml(formatBytes(total))}</p>
    </div>
    <div class="card p-4 flex flex-col gap-3">
      <div class="storage-usage-bar" role="img" aria-label="${escapeHtml(`存储空间占用：${summary}`)}">${segments}</div>
      <ul class="storage-usage-legend">${items.map((item) => `<li data-storage-category="${escapeHtml(item.key)}">
        <span class="storage-usage-swatch" aria-hidden="true"></span>
        <span class="storage-usage-name">${escapeHtml(item.label)}</span>
        <span class="storage-usage-size">${escapeHtml(formatBytes(item.bytes))} · ${escapeHtml(percent(item.bytes, total))}</span>
      </li>`).join('')}</ul>
      <p class="text-xs text-muted">按实例统计的数据估算值，不含数据库索引与文件系统开销。</p>
    </div>
  </section>`;
}
