// Floating preview for image placeholders rendered by renderMessageContent.
// The <img data-auth-src> is fetched with the management credentials by
// mountAuthenticatedResources, and only once the pointer actually hovers.
export function mountImageHoverPreview(): void {
  let popover: HTMLElement | null = null;

  const hide = () => {
    popover?.remove();
    popover = null;
  };

  const show = (anchor: HTMLElement) => {
    const url = anchor.dataset.imageUrl;
    if (!url) return;
    hide();
    const element = document.createElement('div');
    element.className = 'msg-image-hover';
    const image = document.createElement('img');
    image.alt = '图片加载中…';
    image.decoding = 'async';
    image.dataset.authSrc = url;
    element.append(image);
    document.body.append(element);
    popover = element;

    const place = () => {
      const rect = anchor.getBoundingClientRect();
      const width = element.offsetWidth;
      const height = element.offsetHeight;
      const left = Math.max(8, Math.min(rect.left, window.innerWidth - width - 8));
      const below = rect.bottom + 6;
      const top = below + height > window.innerHeight - 8 ? Math.max(8, rect.top - height - 6) : below;
      element.style.left = `${left}px`;
      element.style.top = `${top}px`;
    };
    place();
    image.addEventListener('load', place);
  };

  const anchorOf = (event: Event) =>
    event.target instanceof Element ? event.target.closest<HTMLElement>('.msg-image-ref') : null;

  document.addEventListener('pointerover', (event) => {
    const anchor = anchorOf(event);
    if (anchor) show(anchor);
  });
  document.addEventListener('pointerout', (event) => {
    const anchor = anchorOf(event);
    if (anchor && !anchor.contains(event.relatedTarget as Node | null)) hide();
  });
  document.addEventListener('focusin', (event) => {
    const anchor = anchorOf(event);
    if (anchor) show(anchor);
  });
  document.addEventListener('focusout', (event) => {
    if (anchorOf(event)) hide();
  });
  window.addEventListener('scroll', hide, true);
}
