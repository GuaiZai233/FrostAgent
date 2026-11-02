import { icon } from '../components/icons';
import { escapeHtml } from '../utils/formatters';

type ToolCapability = { name: string; description: string };
const previewLimit = 10;

function renderTool(tool: ToolCapability, preview: boolean): string {
  return `<article class="card overview-tool-card p-3.5 flex flex-col gap-2${preview ? ' overview-tool-preview' : ''}">
    <div class="flex items-center gap-2">
      <span class="text-primary flex items-center">${icon('wrench', 'w-4 h-4')}</span>
      <h3 class="text-xs font-semibold text-foreground">${escapeHtml(tool.name)}</h3>
    </div>
    <p class="text-xs text-muted leading-relaxed">${escapeHtml(tool.description || '无描述')}</p>
  </article>`;
}

export function renderOverviewTools(tools: readonly ToolCapability[], preview = true): string {
  if (!tools.length) return '<p class="text-xs text-muted p-4">暂无已注册工具。</p>';
  const visible = preview ? tools.slice(0, previewLimit) : tools;
  return visible.map((tool) => renderTool(tool, preview)).join('') +
    (preview && tools.length > previewLimit
      ? `<button type="button" id="overview-all-tools" class="card overview-tool-card overview-tools-more text-primary">
          ${icon('grid', 'w-5 h-5')}<span>展示所有</span><span class="text-xs text-muted">共 ${tools.length} 个工具</span>
        </button>`
      : '');
}
