import type { InstanceInfo } from '../instance-state';
import { escapeHtml } from '../utils/formatters';

export function renderInstanceStatus(enabled: boolean): string {
  return `<span class="instance-dot ${enabled ? 'on' : 'off'}" role="img" aria-label="${enabled ? '已启用' : '已停用'}" title="${enabled ? '已启用' : '已停用'}"></span>`;
}

export function renderInstanceLabel(instance: InstanceInfo | null): string {
  if (!instance) return '<span class="instance-selected-name">未选择</span>';
  return `${renderInstanceStatus(instance.enabled)}<span class="instance-selected-name">${escapeHtml(instance.name)}</span>`;
}
