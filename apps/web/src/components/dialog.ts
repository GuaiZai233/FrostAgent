import { icon } from './icons';
import { escapeHtml } from '../utils/formatters';

export interface DialogOptions<T = unknown> {
  title: string;
  description?: string;
  bodyHtml?: string;
  footerHtml?: string;
  maxWidth?: string;
  onMount?: (dialogEl: HTMLDialogElement, close: (result?: T | null) => void) => void;
  onClose?: (result?: T | null) => void;
}

export function openDialog<T = unknown>(options: DialogOptions<T>): Promise<T | null> {
  return new Promise((resolve) => {
    const dialog = document.createElement('dialog');
    dialog.className = 'dialog-modal';
    if (options.maxWidth) {
      dialog.style.maxWidth = options.maxWidth;
    }

    dialog.innerHTML = `
      <div class="dialog-header">
        <div>
          <h2 class="dialog-title">${escapeHtml(options.title)}</h2>
          ${options.description ? `<p class="dialog-description">${escapeHtml(options.description)}</p>` : ''}
        </div>
        <button class="btn btn-ghost btn-icon-sm dialog-close-btn" aria-label="关闭">
          ${icon('close', 'size-4')}
        </button>
      </div>
      <div class="dialog-body">
        ${options.bodyHtml || ''}
      </div>
      ${options.footerHtml ? `<div class="dialog-footer">${options.footerHtml}</div>` : ''}
    `;

    document.body.appendChild(dialog);

    let settled = false;
    const finish = (result: T | null = null) => {
      if (settled) return;
      settled = true;
      try {
        dialog.close();
      } catch {
        // Dialog already closed or not in DOM
      }
      dialog.remove();
      options.onClose?.(result);
      resolve(result);
    };

    const closeBtn = dialog.querySelector('.dialog-close-btn');
    closeBtn?.addEventListener('click', () => finish(null));

    dialog.addEventListener('cancel', (e) => {
      e.preventDefault();
      finish(null);
    });

    // Close when clicking on the backdrop
    dialog.addEventListener('click', (e) => {
      const rect = dialog.getBoundingClientRect();
      const isInDialog =
        rect.top <= e.clientY &&
        e.clientY <= rect.top + rect.height &&
        rect.left <= e.clientX &&
        e.clientX <= rect.width + rect.left;
      if (!isInDialog) {
        finish(null);
      }
    });

    dialog.showModal();

    if (options.onMount) {
      options.onMount(dialog, finish);
    }
  });
}
