export interface PaginationConfig {
  total: number;
  pageIndex: number;
  pageSize: number;
  pageSizeOptions?: number[];
  canGoBack: boolean;
  canGoNext: boolean;
  loading?: boolean;
}

export function renderPagination(config: PaginationConfig): string {
  const sizes = config.pageSizeOptions || [10, 20, 50, 100];
  const sizeOptions = sizes
    .map((s) => `<option value="${s}" ${s === config.pageSize ? 'selected' : ''}>${s} 条/页</option>`)
    .join('');

  return `
    <footer class="pagination flex items-center justify-between gap-3 px-4 py-2.5 border-t border-border text-xs text-muted-foreground">
      <div class="flex items-center gap-2">
        <span>共 <span class="font-medium text-foreground">${config.total}</span> 条记录</span>
        <span class="text-muted-foreground/40">·</span>
        <span>第 <span class="font-medium text-foreground">${config.pageIndex + 1}</span> 页</span>
      </div>
      <div class="flex items-center gap-2">
        <select class="select h-7 text-xs py-0 px-2 border border-input rounded-md bg-background text-foreground cursor-pointer" data-action="page-size">
          ${sizeOptions}
        </select>
        <button class="btn btn-outline btn-sm h-7 px-2.5 text-xs" data-action="prev-page" ${!config.canGoBack || config.loading ? 'disabled' : ''}>
          上一页
        </button>
        <button class="btn btn-outline btn-sm h-7 px-2.5 text-xs" data-action="next-page" ${!config.canGoNext || config.loading ? 'disabled' : ''}>
          下一页
        </button>
      </div>
    </footer>
  `;
}

export function attachPaginationEvents(
  container: HTMLElement,
  callbacks: {
    onPageSizeChange: (newPageSize: number) => void;
    onPrevPage: () => void;
    onNextPage: () => void;
  },
): void {
  const select = container.querySelector<HTMLSelectElement>('[data-action="page-size"]');
  const prevBtn = container.querySelector<HTMLButtonElement>('[data-action="prev-page"]');
  const nextBtn = container.querySelector<HTMLButtonElement>('[data-action="next-page"]');

  select?.addEventListener('change', (e) => {
    const val = parseInt((e.target as HTMLSelectElement).value, 10);
    if (!isNaN(val)) callbacks.onPageSizeChange(val);
  });

  prevBtn?.addEventListener('click', () => {
    callbacks.onPrevPage();
  });

  nextBtn?.addEventListener('click', () => {
    callbacks.onNextPage();
  });
}
