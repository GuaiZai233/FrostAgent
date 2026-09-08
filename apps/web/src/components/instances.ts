import {
  instanceState,
  instanceRequest,
  type InstanceInfo,
} from '../instance-state';
import { openDialog } from './dialog';
import { escapeHtml } from '../utils/formatters';
import { toast } from './toast';
export async function openInstanceManagement(): Promise<void> {
  try {
    const initial = await instanceState.refresh();
    await openDialog({
      title: '实例管理',
      maxWidth: '34rem',
      bodyHtml: '<div id="instance-list" class="flex flex-col gap-3"></div>',
      footerHtml:
        '<button class="btn btn-primary" id="instance-create">＋ 创建实例</button>',
      onMount(dialog, close) {
        let disposed = false;
        let rendered = '';
        const render = (items: InstanceInfo[]) => {
          if (disposed) return;
          const snapshot = JSON.stringify(items);
          if (snapshot === rendered) return;
          rendered = snapshot;
          dialog.querySelector('#instance-list')!.innerHTML =
            items
              .map(
                (item) => `
      <article class="card p-3 instance-item">
       <button class="btn btn-ghost instance-select" data-id="${item.id}">
        <span class="instance-dot ${item.enabled ? 'on' : 'off'}" aria-label="${item.enabled ? '已启用' : '已停用'}"></span>
        <span class="text-left"><strong>${escapeHtml(item.name)}</strong><br><small class="font-mono text-muted">${item.id}</small></span>
       </button>
       <button class="btn btn-ghost btn-icon-sm" data-rename="${item.id}" aria-label="重命名 ${escapeHtml(item.name)}">✎</button>
       <button class="btn btn-ghost btn-icon-sm instance-delete" data-delete="${item.id}" aria-label="删除 ${escapeHtml(item.name)}">×</button>
       ${item.error ? `<p class="text-xs text-destructive">${escapeHtml(item.error)}</p>` : ''}
      </article>`,
              )
              .join('') ||
            '<p class="text-muted text-sm">暂无实例。FrostAgent 正在空跑，可以随时创建实例。</p>';
          dialog.querySelectorAll<HTMLButtonElement>('[data-id]').forEach(
            (btn) =>
              (btn.onclick = () => {
                const item = items.find((i) => i.id === btn.dataset.id)!;
                close();
                instanceState.select(item);
              }),
          );
          dialog.querySelectorAll<HTMLButtonElement>('[data-rename]').forEach(
            (btn) =>
              (btn.onclick = () => {
                void nameDialog(
                  items.find((i) => i.id === btn.dataset.rename)!,
                ).then(refresh);
              }),
          );
          dialog.querySelectorAll<HTMLButtonElement>('[data-delete]').forEach(
            (btn) =>
              (btn.onclick = () => {
                void deleteDialog(
                  items.find((i) => i.id === btn.dataset.delete)!,
                ).then(refresh);
              }),
          );
        };
        const refresh = async () => {
          try {
            const data = await instanceState.refresh();
            render(data.instances);
          } catch (err) {
            toast.error(String(err));
          }
        };
        dialog.querySelector<HTMLButtonElement>('#instance-create')!.onclick =
          () => {
            void nameDialog().then(refresh);
          };
        render(initial.instances);
        const timer = window.setInterval(() => void refresh(), 3000);
        dialog.addEventListener(
          'close',
          () => {
            disposed = true;
            clearInterval(timer);
          },
          { once: true },
        );
      },
    });
  } catch (err) {
    toast.error(String(err));
  }
}
async function nameDialog(item?: InstanceInfo): Promise<void> {
  const state = await instanceState.refresh();
  await openDialog({
    title: item ? '重命名实例' : '创建实例',
    bodyHtml: `<label class="form-label" for="instance-name">实例名称</label><input class="input" id="instance-name" maxlength="32" value="${escapeHtml(item?.name || state.next_name)}"><p class="text-xs text-muted">1–32 个字符，名称不可重复。</p>`,
    footerHtml:
      '<button class="btn btn-primary" id="instance-save">保存</button>',
    onMount(dialog, close) {
      const save = async () => {
        try {
          const name =
            dialog.querySelector<HTMLInputElement>('#instance-name')!.value;
          await instanceRequest(item ? '/' + item.id + '/rename' : '', {
            name,
          });
          close();
        } catch (err) {
          toast.error(String(err));
        }
      };
      dialog.querySelector<HTMLButtonElement>('#instance-save')!.onclick = () =>
        void save();
      dialog.querySelector<HTMLInputElement>('#instance-name')!.onkeydown = (
        e,
      ) => {
        if (e.key === 'Enter') void save();
      };
    },
  });
}
async function deleteDialog(item: InstanceInfo): Promise<void> {
  await openDialog({
    title: '确定删除实例？一切配置不可恢复！',
    description: item.name + ' (' + item.id + ')',
    bodyHtml:
      '<label class="flex items-center gap-2 text-sm"><input type="checkbox" class="checkbox" id="instance-delete-all">删除此实例的所有数据</label>',
    footerHtml:
      '<button class="btn btn-outline dialog-close-btn">取消</button><button class="btn btn-destructive" id="instance-confirm-delete">删除</button>',
    onMount(dialog, close) {
      dialog.querySelector<HTMLButtonElement>(
        '#instance-confirm-delete',
      )!.onclick = () => {
        void (async () => {
          try {
            await instanceRequest('/' + item.id + '/delete', {
              all: dialog.querySelector<HTMLInputElement>(
                '#instance-delete-all',
              )!.checked,
            });
            close();
            if (instanceState.selected?.id === item.id)
              instanceState.select(null);
          } catch (err) {
            toast.error(String(err));
          }
        })();
      };
    },
  });
}
export async function openQuickConfig(): Promise<void> {
  const target = instanceState.selected;
  if (!target) return;
  try {
    const data = await instanceState.refresh();
    await openDialog({
      title: '将复用的实例配置',
      description: '一旦选择，现有配置将完全被选中的实例覆盖！',
      bodyHtml: `<select class="select" id="instance-copy-source"><option value="">请选择实例</option>${data.instances
        .filter((i) => i.id !== target.id)
        .map(
          (i) =>
            `<option value="${i.id}">${escapeHtml(i.name)} (${i.id})</option>`,
        )
        .join(
          '',
        )}</select><p class="text-xs text-muted">仅复用 Settings 与已发布的模型配置。被覆盖的实例必须先停用。</p>`,
      footerHtml:
        '<button class="btn btn-primary" id="instance-copy-confirm">覆盖配置</button>',
      onMount(dialog, close) {
        dialog.querySelector<HTMLButtonElement>(
          '#instance-copy-confirm',
        )!.onclick = () => {
          void (async () => {
            try {
              const source = dialog.querySelector<HTMLSelectElement>(
                '#instance-copy-source',
              )!.value;
              if (!source) {
                toast.error('请选择实例');
                return;
              }
              await instanceRequest('/' + target.id + '/copy', { source });
              close();
              toast.success('实例配置已覆盖');
            } catch (err) {
              toast.error(String(err));
            }
          })();
        };
      },
    });
  } catch (err) {
    toast.error(String(err));
  }
}
