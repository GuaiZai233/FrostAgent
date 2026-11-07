import type { TokenUsageCounter, TokenUsageModule, TokenUsageOverview } from '@frostagent/proto';
import { escapeHtml, formatCount } from '../utils/formatters';

const moduleLabels: Record<string, string> = {
  dialogue: '对话', subagent: '子智能体', vision: '多模态理解',
  group_compact: '群聊摘要', sticker_summary: '表情包摘要',
  memory_extract: '记忆提取', reflection: '记忆整理', embedding: '向量化',
  security: '安全分类',
};
type Counter = Pick<TokenUsageCounter, 'value' | 'unestimatedRequests'>;
type CounterKey = 'input' | 'output' | 'total' | 'cached' | 'cacheHits';

function sum(rows: TokenUsageModule[], key: CounterKey): Counter {
  return rows.reduce((result, row) => ({
    value: result.value + (row[key]?.value ?? 0n),
    unestimatedRequests: result.unestimatedRequests + (row[key]?.unestimatedRequests ?? row.requests),
  }), { value: 0n, unestimatedRequests: 0n });
}

function renderCounter(counter: Counter | undefined, requests: bigint): string {
  if (!counter) return '<span class="text-muted">unestimated</span>';
  const known = counter.unestimatedRequests < requests || requests === 0n;
  const value = known ? escapeHtml(formatCount(counter.value)) : '';
  if (counter.unestimatedRequests === 0n) return value;
  return `${value ? `${value} + ` : ''}<span class="token-usage-unknown" title="${escapeHtml(formatCount(counter.unestimatedRequests))} 次请求未返回此项用量">unestimated <small>(${escapeHtml(formatCount(counter.unestimatedRequests))} 次)</small></span>`;
}

export function renderTokenUsage(usage: TokenUsageOverview | undefined): string {
  if (!usage?.available) {
    return `<section class="flex flex-col gap-3" aria-labelledby="token-usage-title">
      <h2 id="token-usage-title" class="text-sm font-semibold">Token 消耗一览</h2>
      <div class="card p-4 text-sm text-muted">unestimated · 暂时无法读取用量统计</div>
    </section>`;
  }
  const rows = usage.modules;
  const requests = rows.reduce((value, row) => value + row.requests, 0n);
  const summaryRows = rows.filter((row) => ['group_compact', 'sticker_summary', 'memory_extract', 'reflection'].includes(row.module));
  const cards: Array<{ label: string; rows: TokenUsageModule[]; key: CounterKey; hint: string }> = [
    { label: '累计 Token', rows, key: 'total', hint: '输入与输出合计' },
    { label: '对话', rows: rows.filter((row) => ['dialogue', 'subagent'].includes(row.module)), key: 'total', hint: '包含子智能体' },
    { label: '多模态', rows: rows.filter((row) => row.module === 'vision'), key: 'total', hint: '多模态理解请求的完整用量' },
    { label: '摘要（含表情包）', rows: summaryRows, key: 'total', hint: '包含群聊摘要、表情包与记忆整理' },
    { label: '缓存命中 Token', rows, key: 'cached', hint: '已包含在输入中，不重复计入累计' },
    { label: '缓存命中次数', rows, key: 'cacheHits', hint: 'API 返回缓存命中 Token > 0 的请求数' },
  ];
  return `<section class="flex flex-col gap-3" aria-labelledby="token-usage-title">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h2 id="token-usage-title" class="text-sm font-semibold text-foreground">Token 消耗一览</h2>
      <span class="text-xs text-muted">自功能启用起累计 · 重启保留 · ${escapeHtml(formatCount(requests))} 次请求</span>
    </div>
    ${usage.incomplete ? '<p class="text-xs text-warning" role="status">部分用量未能保存，累计数据可能不完整。</p>' : ''}
    <div class="token-usage-grid">${cards.map((card) => {
      const count = card.rows.reduce((value, row) => value + row.requests, 0n);
      return `<article class="card p-3.5 flex flex-col gap-2">
        <h3 class="text-xs font-medium text-muted">${card.label}</h3>
        <p class="token-usage-value font-bold text-foreground">${renderCounter(sum(card.rows, card.key), count)}</p>
        <p class="text-xs text-muted">${card.hint}</p>
      </article>`;
    }).join('')}</div>
    <div class="card token-usage-table-wrap" role="region" aria-label="各模块 Token 消耗" tabindex="0">
      <table class="token-usage-table">
        <thead><tr><th scope="col">模块</th><th scope="col">请求数</th><th scope="col">输入 Token</th><th scope="col">输出 Token</th><th scope="col">合计 Token</th><th scope="col">缓存命中 Token</th><th scope="col">缓存命中次数</th></tr></thead>
        <tbody>${rows.map((row) => `<tr data-usage-module="${escapeHtml(row.module)}">
          <th scope="row">${escapeHtml(moduleLabels[row.module] ?? row.module)}</th>
          <td>${escapeHtml(formatCount(row.requests))}</td>
          ${(['input', 'output', 'total', 'cached', 'cacheHits'] as const).map((key) => `<td>${renderCounter(row[key], row.requests)}</td>`).join('')}
        </tr>`).join('')}</tbody>
      </table>
    </div>
    <p class="text-xs text-muted">依据 API 返回的 usage 统计；未返回的字段标记为 unestimated。存在缺失时保留已知数量，并显示缺失请求数；缓存命中属于输入 Token 的一部分。</p>
  </section>`;
}
