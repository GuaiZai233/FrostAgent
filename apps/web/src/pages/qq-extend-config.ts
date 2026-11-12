import { openDialog } from '../components/dialog';
import { escapeHtml } from '../utils/formatters';

export const qqExtendGroupsKey = 'QQ_EXTEND_MESSAGES_TARGET_GROUPS';

export async function openQQExtendConfig(options: {
  value: string;
  save: (value: string) => Promise<void>;
  mounted: (close: () => void) => void;
  closed: () => void;
  active: () => boolean;
}): Promise<void> {
  let saving = false;
  await openDialog({
    title: '配置 qq_extend_messages',
    description: '跨会话发送目标群',
    canDismiss: () => !saving,
    bodyHtml: `<form id="qq-extend-form" class="flex flex-col gap-4">
      <div class="form-group"><label class="form-label" for="qq-extend-groups">允许发送到的群号白名单</label>
      <textarea id="qq-extend-groups" class="textarea font-mono" rows="6" placeholder="每行一个群号，也可以用逗号分隔">${escapeHtml(options.value)}</textarea></div>
      <p class="text-xs text-muted">允许将当前群聊或私聊的内容发送、转发到这些群。保存后立即生效；留空禁止跨会话发送，当前会话内的功能仍可使用。</p>
      <p id="qq-extend-error" role="alert" class="text-sm text-destructive"></p>
    </form>`,
    footerHtml: '<button type="button" class="btn btn-outline dialog-close-btn">取消</button><button type="submit" form="qq-extend-form" id="qq-extend-save" class="btn btn-primary">保存</button>',
    onMount: (dialog, close) => {
      options.mounted(close);
      const input = dialog.querySelector<HTMLTextAreaElement>('#qq-extend-groups')!;
      const error = dialog.querySelector<HTMLElement>('#qq-extend-error')!;
      const save = dialog.querySelector<HTMLButtonElement>('#qq-extend-save')!;
      dialog.querySelector<HTMLFormElement>('#qq-extend-form')!.onsubmit = async (event) => {
        event.preventDefault();
        if (saving) return;
        error.textContent = '';
        const groups = [...new Set(input.value.split(/[,;\s]+/).filter(Boolean))];
        if (groups.some((id) => !/^[1-9]\d*$/.test(id) || BigInt(id) > 9223372036854775807n)) {
          error.textContent = '请输入有效的正整数群号，使用逗号或换行分隔。';
          return;
        }
        saving = true;
        save.disabled = input.disabled = true;
        try {
          await options.save(groups.join(','));
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
