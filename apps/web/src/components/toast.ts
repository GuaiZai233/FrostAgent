import { icon } from './icons';
import { escapeHtml } from '../utils/formatters';

export type ToastType = 'success' | 'error' | 'warning' | 'info';

export interface ToastOptions {
  duration?: number;
  title?: string;
}

let container: HTMLElement | null = null;

function getContainer(): HTMLElement {
  if (!container) {
    container = document.getElementById('toast-container');
    if (!container) {
      container = document.createElement('div');
      container.id = 'toast-container';
      document.body.appendChild(container);
    }
  }
  container.setAttribute('popover', 'manual');
  if ('showPopover' in container) {
    try {
      if (container.matches(':popover-open')) container.hidePopover();
      container.showPopover();
    } catch {
      // Older browsers keep the fixed-position fallback.
    }
  }
  return container;
}

function showToast(message: string, type: ToastType = 'info', options: ToastOptions = {}): void {
  const root = getContainer();
  const duration = options.duration ?? (type === 'error' ? 5000 : 3000);

  const toastEl = document.createElement('div');
  toastEl.className = `fa-toast fa-toast-${type}`;

  const iconName =
    type === 'success'
      ? 'check_circle'
      : type === 'error'
      ? 'error'
      : type === 'warning'
      ? 'warning'
      : 'info';

  toastEl.innerHTML = `
    <div class="fa-toast-icon">${icon(iconName)}</div>
    <div class="fa-toast-content">
      ${options.title ? `<div class="fa-toast-title">${escapeHtml(options.title)}</div>` : ''}
      <div class="fa-toast-message">${escapeHtml(message)}</div>
    </div>
    <button class="btn btn-ghost btn-icon-sm fa-toast-close" aria-label="关闭">
      ${icon('close', 'sm')}
    </button>
  `;

  const closeBtn = toastEl.querySelector('.fa-toast-close');
  let timeoutId: number | null = null;

  const dismiss = () => {
    if (timeoutId) clearTimeout(timeoutId);
    toastEl.classList.add('fa-toast-closing');
    toastEl.addEventListener('animationend', () => {
      toastEl.remove();
    }, { once: true });
  };

  closeBtn?.addEventListener('click', dismiss);

  if (duration > 0) {
    timeoutId = window.setTimeout(dismiss, duration);
  }

  root.appendChild(toastEl);
}

export const toast = {
  success(message: string, options?: ToastOptions) {
    showToast(message, 'success', options);
  },
  error(message: string, options?: ToastOptions) {
    showToast(message, 'error', options);
  },
  warning(message: string, options?: ToastOptions) {
    showToast(message, 'warning', options);
  },
  info(message: string, options?: ToastOptions) {
    showToast(message, 'info', options);
  },
};
