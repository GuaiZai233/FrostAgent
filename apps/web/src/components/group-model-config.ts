import { create } from '@bufbuild/protobuf';
import { GroupModelOverrideSchema, ModelWorkload } from '@frostagent/proto';
import type { createInstanceAPI } from '../api/client';
import { bindingFor, bindingOptions, setBinding, workloads } from '../utils/model-bindings';
import { openDialog } from './dialog';
import { toast } from './toast';

type ModelRouterAPI = Pick<ReturnType<typeof createInstanceAPI>,
  'getModelRouterState' | 'saveModelRouterDraft' | 'publishModelRouter'>;

const publicationQueues = new WeakMap<ModelRouterAPI, Promise<void>>();

function publishInOrder(api: ModelRouterAPI, action: () => Promise<void>): Promise<void> {
  const previous = publicationQueues.get(api) ?? Promise.resolve();
  const pending = previous.then(action);
  publicationQueues.set(api, pending.then(() => undefined, () => undefined));
  return pending;
}

function canonicalPlatform(platform: string): string {
  const normalized = platform.trim().toLowerCase();
  return ['', 'qq', 'onebot', 'aiocqhttp'].includes(normalized) ? 'qq' : normalized;
}

export function openGroupModelConfig(
  platform: string,
  groupId: string,
  api: ModelRouterAPI,
  onPublished: () => void,
): () => void {
  let closed = false;
  let busy = false;
  let publishing = false;
  let closeDialog: (() => void) | undefined;
  void openDialog({
    title: '群级模型配置',
    description: `群 ${groupId} · 未覆盖的槽位继承实例默认配置。`,
    maxWidth: '38rem',
    bodyHtml: '<div class="text-center text-muted p-6"><span class="spinner"></span> 正在加载模型配置...</div>',
    footerHtml: '<button class="btn btn-outline btn-sm dialog-close-btn">取消</button><button class="btn btn-primary btn-sm" id="group-model-publish" disabled>保存并发布</button>',
    canDismiss: () => !publishing,
    onClose: () => { closed = true; },
    onMount: (dialog, close) => {
      closeDialog = close;
      const body = dialog.querySelector<HTMLElement>('.dialog-body')!;
      const publishButton = dialog.querySelector<HTMLButtonElement>('#group-model-publish')!;
      const setPublishing = (value: boolean) => {
        publishing = value;
        dialog.querySelectorAll<HTMLButtonElement>('.dialog-close-btn').forEach((button) => { button.disabled = value; });
      };
      const load = async () => {
        try {
          const state = await api.getModelRouterState();
          if (closed) return;
          if (state.loadError || !state.active) throw new Error(state.loadError || '缺少活动模型配置');
          // Only published routes are the baseline; unrelated drafts must not
          // be published by a group-specific edit.
          const configuration = structuredClone(state.active);
          const index = configuration.groupOverrides.findIndex((group) =>
            canonicalPlatform(group.platform) === canonicalPlatform(platform) && group.groupId === groupId);
          const group = index >= 0
            ? structuredClone(configuration.groupOverrides[index])
            : create(GroupModelOverrideSchema, { platform: platform || 'onebot', groupId });
          body.innerHTML = workloads.map((workload) => `
            <div class="form-group">
              <label class="form-label" for="group-model-${workload.value}">${workload.label}</label>
              <select class="select group-model-binding" id="group-model-${workload.value}" data-workload="${workload.value}">${bindingOptions(configuration, bindingFor(group.bindings, workload.value), true, '继承实例默认', workload.value !== ModelWorkload.REFLECTION, workload.value !== ModelWorkload.DIALOGUE)}</select>
            </div>`).join('');
          publishButton.disabled = false;
          publishButton.addEventListener('click', async () => {
            if (closed || busy) return;
            busy = true;
            publishButton.disabled = true;
            publishButton.textContent = '保存中...';
            body.querySelectorAll<HTMLSelectElement>('.group-model-binding').forEach((select) => {
              setBinding(group.bindings, Number(select.dataset.workload) as ModelWorkload, select.value, true);
              select.disabled = true;
            });
            const updated = structuredClone(configuration);
            if (index >= 0) {
              if (group.bindings.length) updated.groupOverrides[index] = group;
              else updated.groupOverrides.splice(index, 1);
            } else if (group.bindings.length) {
              updated.groupOverrides.push(group);
            }
            try {
              await publishInOrder(api, async () => {
                if (closed) return;
                const saved = await api.saveModelRouterDraft(updated);
                if (closed) return;
                if (!saved.success || !saved.draft) throw new Error(saved.error || '保存群级模型配置失败');
                setPublishing(true);
                publishButton.textContent = '发布中...';
                const published = await api.publishModelRouter(updated);
                if (closed) return;
                if (!published.success || !published.active) throw new Error(published.error || '发布群级模型配置失败');
                close();
                toast.success('群级模型配置已发布');
                onPublished();
              });
            } catch (err) {
              if (!closed) toast.error('保存群级模型配置失败: ' + errorText(err));
            } finally {
              if (!closed) {
                setPublishing(false);
                busy = false;
                publishButton.disabled = false;
                publishButton.textContent = '保存并发布';
                body.querySelectorAll<HTMLSelectElement>('.group-model-binding').forEach((select) => { select.disabled = false; });
              }
            }
          });
        } catch (err) {
          if (closed) return;
          body.textContent = '模型配置加载失败，请关闭后重试。';
          toast.error('加载群级模型配置失败: ' + errorText(err));
        }
      };
      void load();
    },
  });
  return () => closeDialog?.();
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
