import { openDialog } from './dialog';
import { escapeHtml } from '../utils/formatters';

export interface ConfirmOptions {
  title: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  destructive?: boolean;
  signal?: AbortSignal;
}

export function confirmDialog(options: ConfirmOptions): Promise<boolean> {
  const confirmText = options.confirmLabel || '确认';
  const cancelText = options.cancelLabel || '取消';
  const btnClass = options.destructive ? 'btn-destructive' : 'btn-primary';
  let abortHandler: (() => void) | null = null;

  return openDialog<boolean>({
    title: options.title,
    maxWidth: '26rem',
    bodyHtml: `<p class="text-sm text-muted-foreground leading-relaxed">${escapeHtml(options.message)}</p>`,
    footerHtml: `
      <button class="btn btn-outline btn-sm" id="confirm-cancel-btn">${escapeHtml(cancelText)}</button>
      <button class="btn ${btnClass} btn-sm" id="confirm-ok-btn">${escapeHtml(confirmText)}</button>
    `,
    onMount: (dialogEl, close) => {
      abortHandler = () => close(false);
      if (options.signal?.aborted) {
        abortHandler();
        return;
      }
      options.signal?.addEventListener('abort', abortHandler);
      const cancelBtn = dialogEl.querySelector('#confirm-cancel-btn');
      const okBtn = dialogEl.querySelector('#confirm-ok-btn');

      cancelBtn?.addEventListener('click', () => close(false));
      okBtn?.addEventListener('click', () => close(true));
    },
    onClose: () => {
      if (abortHandler) options.signal?.removeEventListener('abort', abortHandler);
    },
  }).then((res) => Boolean(res));
}
