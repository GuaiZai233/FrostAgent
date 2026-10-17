import { create } from '@bufbuild/protobuf';
import {
  ModelBindingMode,
  ModelBindingSchema,
  ModelWorkload,
  type ModelBinding,
  type ModelRouterConfiguration,
} from '@frostagent/proto';
import { escapeHtml } from './formatters';

export const workloads = [
  { value: ModelWorkload.DIALOGUE, label: '主对话' },
  { value: ModelWorkload.SUBAGENT, label: '子 Agent' },
  { value: ModelWorkload.VISION, label: '多模态（图片）' },
  { value: ModelWorkload.REFLECTION, label: '反思' },
  { value: ModelWorkload.MEMORY_EXTRACT, label: '记忆提取' },
  { value: ModelWorkload.GROUP_COMPACT, label: '群聊压缩' },
  { value: ModelWorkload.EMBEDDING, label: 'Embedding' },
] as const;

export function bindingFor(bindings: ModelBinding[], workload: ModelWorkload): ModelBinding | undefined {
  return bindings.find((binding) => binding.workload === workload);
}

export function bindingOptions(
  configuration: ModelRouterConfiguration,
  binding: ModelBinding | undefined,
  allowInherit: boolean,
  inheritLabel = '继承实例默认',
  allowDisabled = true,
  allowFollowDialogue = false,
): string {
  const selectedMode = binding?.mode ?? (
    allowInherit
      ? ModelBindingMode.INHERIT
      : allowFollowDialogue && !allowDisabled
        ? ModelBindingMode.FOLLOW_DIALOGUE
        : ModelBindingMode.DISABLED
  );
  const selectedModel = binding?.modelId || '';
  const items: string[] = [];
  if (allowInherit) items.push(`<option value="inherit" ${selectedMode === ModelBindingMode.INHERIT ? 'selected' : ''}>${escapeHtml(inheritLabel)}</option>`);
  if (allowFollowDialogue) items.push(`<option value="follow-dialogue" ${selectedMode === ModelBindingMode.FOLLOW_DIALOGUE ? 'selected' : ''}>跟随主对话</option>`);
  if (allowDisabled) items.push(`<option value="disabled" ${selectedMode === ModelBindingMode.DISABLED ? 'selected' : ''}>Disabled</option>`);
  for (const model of configuration.models) {
    const endpoint = configuration.endpoints.find((item) => item.id === model.endpointId);
    const label = `${model.displayName} · ${endpoint?.displayName || '未知 Endpoint'} · ${model.upstreamModel}`;
    items.push(`<option value="model:${escapeHtml(model.id)}" ${selectedMode === ModelBindingMode.MODEL && selectedModel === model.id ? 'selected' : ''}>${escapeHtml(label)}</option>`);
  }
  return items.join('');
}

export function setBinding(bindings: ModelBinding[], workload: ModelWorkload, value: string, allowInherit: boolean) {
  const index = bindings.findIndex((binding) => binding.workload === workload);
  if (value === 'inherit' && allowInherit) {
    if (index >= 0) bindings.splice(index, 1);
    return;
  }
  const binding = create(ModelBindingSchema, {
    workload,
    mode: value === 'disabled'
      ? ModelBindingMode.DISABLED
      : value === 'follow-dialogue'
        ? ModelBindingMode.FOLLOW_DIALOGUE
        : ModelBindingMode.MODEL,
    modelId: value.startsWith('model:') ? value.slice(6) : '',
  });
  if (index >= 0) bindings[index] = binding;
  else bindings.push(binding);
}
