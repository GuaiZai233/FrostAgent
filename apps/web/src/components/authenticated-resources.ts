import { controlPlaneFetch, downloadControlPlaneFile } from '../api/control-auth';
import { toast } from './toast';

// Native links and images cannot attach the management bearer header.
export function mountAuthenticatedResources(): void {
  document.addEventListener('click', (event) => {
    const link = event.target instanceof Element ? event.target.closest<HTMLAnchorElement>('a[download]') : null;
    if (!link) return;
    const url = new URL(link.href);
    if (url.origin !== window.location.origin || !url.pathname.startsWith('/api/instances/')) return;
    event.preventDefault();
    void downloadControlPlaneFile(link.href, link.download).catch((error) => toast.error('下载失败: ' + String(error)));
  });

  const images = new Map<HTMLImageElement, { controller: AbortController; url?: string }>();
  const observer = new MutationObserver(() => {
    for (const [image, request] of images) {
      if (!image.isConnected) {
        request.controller.abort();
        if (request.url) URL.revokeObjectURL(request.url);
        images.delete(image);
      }
    }
    document.querySelectorAll<HTMLImageElement>('img[data-auth-src]').forEach((image) => {
      if (images.has(image)) return;
      const source = image.dataset.authSrc!;
      const request: { controller: AbortController; url?: string } = { controller: new AbortController() };
      images.set(image, request);
      void controlPlaneFetch(source, { signal: request.controller.signal }).then(async (response) => {
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        const blob = await response.blob();
        if (!image.isConnected || request.controller.signal.aborted) return;
        request.url = URL.createObjectURL(blob);
        image.src = request.url;
        const preview = image.closest<HTMLAnchorElement>('a[data-auth-preview]');
        if (preview) preview.href = request.url;
      }).catch(() => {
        if (image.isConnected && !request.controller.signal.aborted) image.alt = '图片加载失败，请检查访问凭据';
      });
    });
  });
  observer.observe(document.body, { childList: true, subtree: true });
}
