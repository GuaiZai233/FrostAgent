import { openDialog } from './dialog';
import { escapeHtml } from '../utils/formatters';

export interface ConfirmOptions {
  title: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  destructive?: boolean;
}

export function confirmDialog(options: ConfirmOptions): Promise<boolean> {
  const confirmText = options.confirmLabel || '确认';
  const cancelText = options.cancelLabel || '取消';
  const btnClass = options.destructive ? 'btn-destructive' : 'btn-primary';

  return openDialog<boolean>({
    title: options.title,
    maxWidth: '28rem',
    bodyHtml: `<p class="text-sm leading-relaxed">${escapeHtml(options.message)}</p>`,
    footerHtml: `
      <button class="btn btn-outline" id="confirm-cancel-btn">${escapeHtml(cancelText)}</button>
      <button class="btn ${btnClass}" id="confirm-ok-btn">${escapeHtml(confirmText)}</button>
    `,
    onMount: (dialogEl, close) => {
      const cancelBtn = dialogEl.querySelector('#confirm-cancel-btn');
      const okBtn = dialogEl.querySelector('#confirm-ok-btn');

      cancelBtn?.addEventListener('click', () => close(false));
      okBtn?.addEventListener('click', () => close(true));
    },
  }).then((res) => Boolean(res));
}
