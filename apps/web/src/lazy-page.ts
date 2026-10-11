import type { PageMountFn } from './router';

export function lazyMount(load: () => Promise<PageMountFn>): PageMountFn {
  return (container) => {
    let disposed = false;
    let cleanup: ReturnType<PageMountFn>;

    container.innerHTML =
      '<div class="page-container p-6 text-muted">页面加载中...</div>';

    void Promise.resolve()
      .then(load)
      .then((mount) => {
        if (disposed) return;
        cleanup = mount(container);
      })
      .catch((error: unknown) => {
        if (disposed) return;
        console.error('Page load error:', error);
        container.innerHTML =
          '<div class="page-container p-6 text-muted">页面加载失败，请刷新重试。</div>';
      });

    return () => {
      disposed = true;
      if (typeof cleanup === 'function') cleanup();
    };
  };
}
