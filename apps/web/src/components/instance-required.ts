import { openInstanceManagement } from './instances';

export function mountInstanceRequired(container: HTMLElement): void {
  container.innerHTML = `<article class="card p-6"><h2 class="text-base font-semibold">请选择实例</h2><p class="text-sm text-muted mt-1">FrostAgent Control Plane 正在运行。创建或选择实例后，即可管理它的配置与数据。</p><button class="btn btn-primary mt-4" id="empty-manage">实例管理</button></article>`;
  container.querySelector('#empty-manage')!.addEventListener('click', () => void openInstanceManagement());
}

// Empty data pages keep their real layout, but do not bind instance operations.
export function disableInstanceControls(container: HTMLElement): void {
  container.querySelectorAll<HTMLInputElement | HTMLButtonElement | HTMLSelectElement | HTMLTextAreaElement>('input, button, select, textarea')
    .forEach((control) => { control.disabled = true; });
}
