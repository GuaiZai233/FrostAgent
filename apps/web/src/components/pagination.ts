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
    .map((s) => `<option value="${s}" ${s === config.pageSize ? 'selected' : ''}>${s}</option>`)
    .join('');

  return `
    <footer class="pagination">
      <div class="pagination-info">
        <span>共 ${config.total} 条</span>
        ·
        <span>第 ${config.pageIndex + 1} 页</span>
      </div>
      <div class="pagination-controls">
        <select class="select" style="width: auto; height: 2rem; padding: 0.25rem 0.5rem; font-size: 0.75rem;" data-action="page-size">
          ${sizeOptions}
        </select>
        <button class="btn btn-outline btn-sm" data-action="prev-page" ${!config.canGoBack || config.loading ? 'disabled' : ''}>
          上一页
        </button>
        <button class="btn btn-outline btn-sm" data-action="next-page" ${!config.canGoNext || config.loading ? 'disabled' : ''}>
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
