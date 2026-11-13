import { openDialog } from '../components/dialog';
import { escapeHtml } from '../utils/formatters';

export const timeSleepTimeoutKey = 'TIME_SLEEP_TIMEOUT_SECONDS';

export async function openTimeSleepConfig(options: {
  value: string;
  save: (value: string) => Promise<void>;
  mounted: (close: () => void) => void;
  closed: () => void;
  active: () => boolean;
}): Promise<void> {
  let saving = false;
  await openDialog({
    title: '配置 time_sleep',
    description: '单次挂起超时',
    canDismiss: () => !saving,
    bodyHtml: `<form id="time-sleep-form" class="flex flex-col gap-4">
      <div class="form-group"><label class="form-label" for="time-sleep-timeout">超时秒数</label>
      <input id="time-sleep-timeout" class="input" type="number" min="0.000000001" step="any" placeholder="60" value="${escapeHtml(options.value)}"></div>
      <p class="text-xs text-muted">留空使用默认值 60 秒。达到上限时工具报错 Timeout；保存后下次调用生效。</p>
      <p id="time-sleep-error" role="alert" class="text-sm text-destructive"></p>
    </form>`,
    footerHtml: '<button type="button" class="btn btn-outline dialog-close-btn">取消</button><button type="submit" form="time-sleep-form" id="time-sleep-save" class="btn btn-primary">保存</button>',
    onMount: (dialog, close) => {
      options.mounted(close);
      const input = dialog.querySelector<HTMLInputElement>('#time-sleep-timeout')!;
      const error = dialog.querySelector<HTMLElement>('#time-sleep-error')!;
      const save = dialog.querySelector<HTMLButtonElement>('#time-sleep-save')!;
      dialog.querySelector<HTMLFormElement>('#time-sleep-form')!.onsubmit = async (event) => {
        event.preventDefault();
        if (saving || !options.active()) return;
        error.textContent = '';
        const value = input.value.trim();
        if (value && (!Number.isFinite(Number(value)) || Number(value) <= 0)) {
          error.textContent = '请输入有效的正数秒数。';
          return;
        }
        saving = true;
        save.disabled = input.disabled = true;
        try {
          await options.save(value);
          if (options.active() && dialog.isConnected) close();
        } catch (err) {
          if (options.active() && dialog.isConnected) error.textContent = String(err);
        } finally {
          saving = false;
          save.disabled = input.disabled = false;
        }
      };
    },
    onClose: options.closed,
  });
}
