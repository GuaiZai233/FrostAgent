import { create } from '@bufbuild/protobuf';
import {
  GroupModelOverrideSchema,
  ModelBindingMode,
  ModelBindingSchema,
  ModelEndpointSchema,
  ModelRouterConfigurationSchema,
  ModelTargetSchema,
  ModelWorkload,
  type ModelBinding,
  type ModelEndpoint,
  type ModelRouterConfiguration,
  type ModelTarget,
} from '@frostagent/proto';
import { api } from '../api/client';
import { openDialog } from '../components/dialog';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { escapeHtml, maskSecret } from '../utils/formatters';

const workloads = [
  { value: ModelWorkload.DIALOGUE, label: '主对话', applied: true },
  { value: ModelWorkload.SUBAGENT, label: '子 Agent', applied: true },
  { value: ModelWorkload.VISION, label: '多模态（图片）', applied: true },
  { value: ModelWorkload.REFLECTION, label: '反思', applied: false },
  { value: ModelWorkload.MEMORY_EXTRACT, label: '记忆提取', applied: false },
  { value: ModelWorkload.GROUP_COMPACT, label: '群聊压缩', applied: true },
] as const;

function newId(prefix: string): string {
  return `${prefix}_${crypto.randomUUID().replaceAll('-', '').slice(0, 16)}`;
}

function cloneConfiguration(config: ModelRouterConfiguration): ModelRouterConfiguration {
  return structuredClone(config);
}

function endpointWarns(baseUrl: string): boolean {
  return baseUrl.trim().replace(/\/+$/, '').toLowerCase().endsWith('/chat/completions');
}

function bindingFor(bindings: ModelBinding[], workload: ModelWorkload): ModelBinding | undefined {
  return bindings.find((binding) => binding.workload === workload);
}

function modelLabel(model: ModelTarget, endpoints: ModelEndpoint[]): string {
  const endpoint = endpoints.find((item) => item.id === model.endpointId);
  return `${model.displayName} · ${endpoint?.displayName || '未知 Endpoint'} · ${model.upstreamModel}`;
}

export function mountModelRouterPage(container: HTMLElement): () => void {
  let unmounted = false;
  let loading = true;
  let busy = false;
  let tab: 'endpoints' | 'models' | 'global' | 'groups' = 'endpoints';
  let draft = create(ModelRouterConfigurationSchema, { version: 1 });
  let activeRevision = 0n;
  let loadError = '';
  let testingModelId = '';
  const revealedKeys = new Set<string>();

  async function load() {
    loading = true;
    render();
    try {
      const state = await api.getModelRouterState();
      if (unmounted) return;
      draft = state.draft
        ? cloneConfiguration(state.draft)
        : create(ModelRouterConfigurationSchema, { version: 1 });
      activeRevision = state.active?.revision ?? 0n;
      loadError = state.loadError;
      loading = false;
      render();
      openRequestedGroup();
    } catch (err) {
      if (unmounted) return;
      loading = false;
      toast.error('加载模型路由配置失败: ' + errorText(err));
      render();
    }
  }

  async function saveDraft(showSuccess = true, updatePage = true): Promise<boolean> {
    if (updatePage) {
      busy = true;
      render();
    }
    try {
      const response = await api.saveModelRouterDraft(draft);
      if (!response.success || !response.draft) {
        toast.error(response.error || '保存草稿失败');
        return false;
      }
      draft = cloneConfiguration(response.draft);
      if (showSuccess) toast.success('草稿已保存到当前进程');
      return true;
    } catch (err) {
      toast.error('保存草稿失败: ' + errorText(err));
      return false;
    } finally {
      if (updatePage) {
        busy = false;
        render();
      }
    }
  }

  async function publish() {
    if (!(await saveDraft(false))) return;
    busy = true;
    render();
    try {
      const response = await api.publishModelRouter();
      if (!response.success || !response.active) {
        toast.error(response.error || '发布配置失败');
        return;
      }
      activeRevision = response.active.revision;
      draft = cloneConfiguration(response.active);
      toast.success(`模型路由配置已发布（revision ${activeRevision}）`);
    } catch (err) {
      toast.error('发布配置失败: ' + errorText(err));
    } finally {
      busy = false;
      render();
    }
  }

  async function discardDraft() {
    busy = true;
    render();
    try {
      const discarded = await api.discardModelRouterDraft();
      if (discarded) draft = cloneConfiguration(discarded);
      toast.info('内存草稿已恢复为活动配置');
    } catch (err) {
      toast.error('放弃草稿失败: ' + errorText(err));
    } finally {
      busy = false;
      render();
    }
  }

  function render() {
    if (loading) {
      container.innerHTML = `<div class="page-container"><div class="card p-6 text-center text-muted"><span class="spinner"></span> 正在加载模型路由器...</div></div>`;
      return;
    }

    container.innerHTML = `
      <div class="page-container fade-in">
        <header class="page-header">
          <div>
            <h1 class="page-title">模型路由器</h1>
            <p class="page-description">可视化管理 OpenAI 兼容 Endpoint、模型和群级路由</p>
          </div>
          <div class="flex items-center gap-2 flex-wrap">
            <span class="badge badge-outline">活动 revision ${activeRevision}</span>
            <button class="btn btn-outline btn-sm" id="router-discard" ${busy ? 'disabled' : ''}>放弃草稿</button>
            <button class="btn btn-secondary btn-sm" id="router-save" ${busy ? 'disabled' : ''}>${icon('save', 'w-3.5 h-3.5')} 保存草稿</button>
            <button class="btn btn-primary btn-sm" id="router-publish" ${busy ? 'disabled' : ''}>${icon('play', 'w-3.5 h-3.5')} 发布配置</button>
          </div>
        </header>

        ${
          loadError
            ? `<div class="card p-3 badge-destructive"><strong>配置文件加载失败：</strong>${escapeHtml(loadError)}。原文件不会被自动覆盖；保存并发布新配置后才能恢复。</div>`
            : ''
        }

        <div class="card p-3 badge-warning text-xs">
          API Key 以明文写入 data/model_router.json，且当前管理面没有鉴权。记忆提取与反思槽仅预留接口，当前运行时不采用。
        </div>

        <div class="tabs" role="tablist">
          ${tabButton('endpoints', 'Endpoint Accounts')}
          ${tabButton('models', 'Models')}
          ${tabButton('global', '全局路由')}
          ${tabButton('groups', '群覆盖')}
        </div>

        <section id="router-tab-content">${renderTab()}</section>
      </div>
    `;

    attachPageEvents();
  }

  function tabButton(value: typeof tab, label: string): string {
    return `<button class="tab-item ${tab === value ? 'active' : ''}" data-router-tab="${value}">${label}</button>`;
  }

  function renderTab(): string {
    switch (tab) {
      case 'models':
        return renderModels();
      case 'global':
        return renderGlobalBindings();
      case 'groups':
        return renderGroups();
      case 'endpoints':
      default:
        return renderEndpoints();
    }
  }

  function renderEndpoints(): string {
    return `
      <div class="flex items-center justify-between gap-3 mb-3">
        <div><h2 class="text-base font-semibold">Endpoint Accounts</h2><p class="text-xs text-muted">一个 Endpoint 可以承载多个模型；空 Key 表示无鉴权。</p></div>
        <button class="btn btn-primary btn-sm" id="add-endpoint">${icon('plus', 'w-3.5 h-3.5')} 新增 Endpoint</button>
      </div>
      <div class="card table-card overflow-hidden">
        <div class="table-container"><table class="table">
          <thead><tr><th>名称</th><th>Base URL</th><th>API Key</th><th>状态</th><th style="text-align:right">操作</th></tr></thead>
          <tbody>
            ${
              draft.endpoints.length
                ? draft.endpoints.map((endpoint) => {
                    const visible = revealedKeys.has(endpoint.id);
                    return `<tr>
                      <td class="font-medium">${escapeHtml(endpoint.displayName)}</td>
                      <td class="font-mono text-xs">${escapeHtml(endpoint.baseUrl)}</td>
                      <td class="font-mono text-xs"><span data-endpoint-key>${escapeHtml(visible ? endpoint.apiKey || '无鉴权' : endpoint.apiKey ? maskSecret(endpoint.apiKey) : '无鉴权')}</span></td>
                      <td><span class="badge ${endpoint.enabled ? 'badge-success' : 'badge-outline'}">${endpoint.enabled ? '启用' : '停用'}</span></td>
                      <td style="text-align:right"><div class="flex justify-end gap-1">
                        <button class="btn btn-ghost btn-icon-sm" data-action="reveal-endpoint" data-id="${escapeHtml(endpoint.id)}" title="${visible ? '隐藏 Key' : '查看 Key'}" aria-label="${visible ? '隐藏 API Key' : '查看 API Key'}" aria-pressed="${visible}">${icon(visible ? 'eye_off' : 'eye')}</button>
                        <button class="btn btn-ghost btn-icon-sm" data-action="edit-endpoint" data-id="${escapeHtml(endpoint.id)}" title="编辑">${icon('edit')}</button>
                        <button class="btn btn-ghost btn-icon-sm text-destructive" data-action="delete-endpoint" data-id="${escapeHtml(endpoint.id)}" title="删除">${icon('trash')}</button>
                      </div></td>
                    </tr>`;
                  }).join('')
                : `<tr><td colspan="5" class="text-center text-muted" style="padding:2.5rem">尚未配置 Endpoint。</td></tr>`
            }
          </tbody>
        </table></div>
      </div>`;
  }

  function renderModels(): string {
    return `
      <div class="flex items-center justify-between gap-3 mb-3">
        <div><h2 class="text-base font-semibold">Models</h2><p class="text-xs text-muted">模型名称可手工填写，也可以从 Endpoint 获取建议。</p></div>
        <button class="btn btn-primary btn-sm" id="add-model" ${draft.endpoints.length ? '' : 'disabled'}>${icon('plus', 'w-3.5 h-3.5')} 新增 Model</button>
      </div>
      <div class="card table-card overflow-hidden"><div class="table-container"><table class="table">
        <thead><tr><th>显示名称</th><th>Endpoint</th><th>上游模型</th><th>能力标签</th><th style="text-align:right">操作</th></tr></thead>
        <tbody>${
          draft.models.length
            ? draft.models.map((model) => {
              const testing = testingModelId === model.id;
              return `<tr>
                <td class="font-medium">${escapeHtml(model.displayName)}</td>
                <td>${escapeHtml(draft.endpoints.find((item) => item.id === model.endpointId)?.displayName || '缺失')}</td>
                <td class="font-mono text-xs">${escapeHtml(model.upstreamModel)}</td>
                <td>${model.capabilities.length ? model.capabilities.map((cap) => `<span class="badge badge-outline mr-1">${escapeHtml(cap)}</span>`).join('') : '<span class="text-muted text-xs">未标记</span>'}</td>
                <td style="text-align:right"><div class="flex justify-end gap-1">
                  <button class="btn btn-ghost btn-sm" data-action="test-model" data-id="${escapeHtml(model.id)}" ${testingModelId ? 'disabled' : ''} aria-busy="${testing}">${testing ? '<span class="spinner inline-block" style="width:0.875rem;height:0.875rem"></span> 测试中...' : `${icon('play', 'w-3.5 h-3.5')} 测试`}</button>
                  <button class="btn btn-ghost btn-icon-sm" data-action="edit-model" data-id="${escapeHtml(model.id)}">${icon('edit')}</button>
                  <button class="btn btn-ghost btn-icon-sm text-destructive" data-action="delete-model" data-id="${escapeHtml(model.id)}">${icon('trash')}</button>
                </div></td>
              </tr>`;
            }).join('')
            : `<tr><td colspan="5" class="text-center text-muted" style="padding:2.5rem">尚未配置 Model。</td></tr>`
        }</tbody>
      </table></div></div>`;
  }

  function renderGlobalBindings(): string {
    return `
      <div class="mb-3"><h2 class="text-base font-semibold">全局六槽配置</h2><p class="text-xs text-muted">私聊和没有群级 diff 的群使用这些默认值。</p></div>
      <div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(19rem,1fr));gap:.75rem">
        ${workloads.map((workload) => {
          const binding = bindingFor(draft.globalBindings, workload.value);
          return `<div class="card p-4">
            <div class="flex items-center justify-between gap-2 mb-3">
              <strong class="text-sm">${workload.label}</strong>
              ${workload.applied ? '<span class="badge badge-success">运行时生效</span>' : '<span class="badge badge-warning">预留，当前不生效</span>'}
            </div>
            <select class="select" data-global-workload="${workload.value}">${bindingOptions(binding, workload.value === ModelWorkload.REFLECTION, workload.value === ModelWorkload.REFLECTION ? '跟随主对话（当前行为）' : '继承全局', workload.value !== ModelWorkload.REFLECTION)}</select>
          </div>`;
        }).join('')}
      </div>`;
  }

  function renderGroups(): string {
    return `
      <div class="flex items-center justify-between gap-3 mb-3">
        <div><h2 class="text-base font-semibold">群级覆盖</h2><p class="text-xs text-muted">仅保存相对于全局配置的 diff；群列表入口同时复用会话管理。</p></div>
        <button class="btn btn-primary btn-sm" id="add-group">${icon('plus', 'w-3.5 h-3.5')} 手动添加</button>
      </div>
      <div class="card table-card overflow-hidden"><div class="table-container"><table class="table">
        <thead><tr><th>平台</th><th>群 ID</th><th>覆盖槽位</th><th style="text-align:right">操作</th></tr></thead>
        <tbody>${
          draft.groupOverrides.length
            ? draft.groupOverrides.map((group) => `<tr>
                <td><span class="badge badge-outline">${escapeHtml(group.platform)}</span></td>
                <td class="font-mono text-xs">${escapeHtml(group.groupId)}</td>
                <td>${group.bindings.length} 个 diff</td>
                <td style="text-align:right"><div class="flex justify-end gap-1">
                  <button class="btn btn-ghost btn-sm" data-action="edit-group" data-platform="${escapeHtml(group.platform)}" data-group-id="${escapeHtml(group.groupId)}">${icon('sliders', 'w-3.5 h-3.5')} 配置</button>
                  <button class="btn btn-ghost btn-icon-sm text-destructive" data-action="delete-group" data-platform="${escapeHtml(group.platform)}" data-group-id="${escapeHtml(group.groupId)}">${icon('trash')}</button>
                </div></td>
              </tr>`).join('')
            : `<tr><td colspan="4" class="text-center text-muted" style="padding:2.5rem">尚无群级覆盖。</td></tr>`
        }</tbody>
      </table></div></div>`;
  }

  function bindingOptions(
    binding: ModelBinding | undefined,
    allowInherit: boolean,
    inheritLabel = '继承全局',
    allowDisabled = true,
  ): string {
    const selectedMode = binding?.mode ?? (allowInherit ? ModelBindingMode.INHERIT : ModelBindingMode.DISABLED);
    const selectedModel = binding?.modelId || '';
    const items: string[] = [];
    if (allowInherit) items.push(`<option value="inherit" ${selectedMode === ModelBindingMode.INHERIT ? 'selected' : ''}>${inheritLabel}</option>`);
    if (allowDisabled) items.push(`<option value="disabled" ${selectedMode === ModelBindingMode.DISABLED ? 'selected' : ''}>Disabled</option>`);
    for (const model of draft.models) {
      items.push(`<option value="model:${escapeHtml(model.id)}" ${selectedMode === ModelBindingMode.MODEL && selectedModel === model.id ? 'selected' : ''}>${escapeHtml(modelLabel(model, draft.endpoints))}</option>`);
    }
    return items.join('');
  }

  function attachPageEvents() {
    container.querySelectorAll<HTMLButtonElement>('[data-router-tab]').forEach((button) => {
      button.addEventListener('click', () => {
        tab = button.dataset.routerTab as typeof tab;
        render();
      });
    });
    container.querySelector('#router-save')?.addEventListener('click', () => void saveDraft());
    container.querySelector('#router-publish')?.addEventListener('click', () => void publish());
    container.querySelector('#router-discard')?.addEventListener('click', () => void discardDraft());
    container.querySelector('#add-endpoint')?.addEventListener('click', () => editEndpoint());
    container.querySelector('#add-model')?.addEventListener('click', () => editModel());
    container.querySelector('#add-group')?.addEventListener('click', () => editGroup());

    container.querySelectorAll<HTMLSelectElement>('[data-global-workload]').forEach((select) => {
      select.addEventListener('change', () => {
        const workload = Number(select.dataset.globalWorkload) as ModelWorkload;
        setBinding(draft.globalBindings, workload, select.value, workload === ModelWorkload.REFLECTION);
        render();
      });
    });

    container.querySelectorAll<HTMLElement>('[data-action]').forEach((element) => {
      element.addEventListener('click', () => handleAction(element));
    });
  }

  function handleAction(element: HTMLElement) {
    const action = element.dataset.action;
    const id = element.dataset.id || '';
    if (action === 'reveal-endpoint') {
      const endpoint = draft.endpoints.find((item) => item.id === id);
      if (!endpoint) return;
      const visible = !revealedKeys.has(id);
      if (visible) revealedKeys.add(id);
      else revealedKeys.delete(id);
      const key = element.closest('tr')?.querySelector<HTMLElement>('[data-endpoint-key]');
      if (key) key.textContent = visible ? endpoint.apiKey || '无鉴权' : endpoint.apiKey ? maskSecret(endpoint.apiKey) : '无鉴权';
      element.innerHTML = icon(visible ? 'eye_off' : 'eye');
      element.title = visible ? '隐藏 Key' : '查看 Key';
      element.setAttribute('aria-label', visible ? '隐藏 API Key' : '查看 API Key');
      element.setAttribute('aria-pressed', String(visible));
    } else if (action === 'edit-endpoint') {
      editEndpoint(id);
    } else if (action === 'delete-endpoint') {
      if (draft.models.some((model) => model.endpointId === id)) {
        toast.error('该 Endpoint 仍被 Model 引用，请先删除或改绑 Model。');
        return;
      }
      draft.endpoints = draft.endpoints.filter((endpoint) => endpoint.id !== id);
      render();
    } else if (action === 'edit-model') {
      editModel(id);
    } else if (action === 'delete-model') {
      if (modelIsReferenced(id)) {
        toast.error('该 Model 仍被路由绑定引用，请先解除引用。');
        return;
      }
      draft.models = draft.models.filter((model) => model.id !== id);
      render();
    } else if (action === 'test-model') {
      void testModel(id);
    } else if (action === 'edit-group') {
      editGroup(element.dataset.platform, element.dataset.groupId);
    } else if (action === 'delete-group') {
      const platform = element.dataset.platform || '';
      const groupId = element.dataset.groupId || '';
      draft.groupOverrides = draft.groupOverrides.filter((group) => group.platform !== platform || group.groupId !== groupId);
      render();
    }
  }

  function editEndpoint(id = '') {
    const existing = draft.endpoints.find((item) => item.id === id);
    const endpoint = existing
      ? structuredClone(existing)
      : create(ModelEndpointSchema, { id: newId('endpoint'), enabled: true });
    void openDialog({
      title: existing ? '编辑 Endpoint' : '新增 Endpoint',
      maxWidth: '34rem',
      bodyHtml: `
        <div class="form-group"><label class="form-label">显示名称</label><input class="input" id="endpoint-name" value="${escapeHtml(endpoint.displayName)}"></div>
        <div class="form-group"><label class="form-label">Base URL</label><input class="input font-mono" id="endpoint-url" value="${escapeHtml(endpoint.baseUrl)}" placeholder="https://example.com/v1"><p class="form-hint text-destructive" id="endpoint-url-warning" style="display:${endpointWarns(endpoint.baseUrl) ? 'block' : 'none'}">这里建议填写 Base URL，也就是不以 /chat/completions 结尾。如果执意继续，很可能引起错误。</p></div>
        <div class="form-group"><label class="form-label">API Key（允许为空）</label><input class="input font-mono" id="endpoint-key" value="${escapeHtml(endpoint.apiKey)}"></div>
        <label class="flex items-center gap-2 text-sm"><input type="checkbox" class="checkbox" id="endpoint-enabled" ${endpoint.enabled ? 'checked' : ''}>启用 Endpoint</label>`,
      footerHtml: `<button class="btn btn-outline btn-sm dialog-close-btn">取消</button><button class="btn btn-primary btn-sm" id="endpoint-confirm">保存到草稿</button>`,
      onMount: (dialog, close) => {
        const urlInput = dialog.querySelector<HTMLInputElement>('#endpoint-url')!;
        const warning = dialog.querySelector<HTMLElement>('#endpoint-url-warning')!;
        urlInput.addEventListener('input', () => warning.style.display = endpointWarns(urlInput.value) ? 'block' : 'none');
        dialog.querySelector('#endpoint-confirm')?.addEventListener('click', () => {
          endpoint.displayName = dialog.querySelector<HTMLInputElement>('#endpoint-name')!.value;
          endpoint.baseUrl = urlInput.value;
          endpoint.apiKey = dialog.querySelector<HTMLInputElement>('#endpoint-key')!.value;
          endpoint.enabled = dialog.querySelector<HTMLInputElement>('#endpoint-enabled')!.checked;
          const index = draft.endpoints.findIndex((item) => item.id === endpoint.id);
          if (index >= 0) draft.endpoints[index] = endpoint;
          else draft.endpoints.push(endpoint);
          close();
          render();
        });
      },
    });
  }

  function editModel(id = '') {
    const existing = draft.models.find((item) => item.id === id);
    const model = existing
      ? structuredClone(existing)
      : create(ModelTargetSchema, { id: newId('model'), enabled: true, endpointId: draft.endpoints[0]?.id || '' });
    void openDialog({
      title: existing ? '编辑 Model' : '新增 Model',
      maxWidth: '36rem',
      bodyHtml: `
        <div class="form-group"><label class="form-label">显示名称</label><input class="input" id="model-name" value="${escapeHtml(model.displayName)}"></div>
        <div class="form-group"><label class="form-label">Endpoint</label><select class="select" id="model-endpoint">${draft.endpoints.map((endpoint) => `<option value="${escapeHtml(endpoint.id)}" ${endpoint.id === model.endpointId ? 'selected' : ''}>${escapeHtml(endpoint.displayName)}</option>`).join('')}</select></div>
        <div class="form-group"><label class="form-label">上游模型名称</label><div class="flex gap-2"><input class="input font-mono" id="model-upstream" value="${escapeHtml(model.upstreamModel)}" style="min-width:0;flex:1"><button type="button" class="btn btn-outline btn-icon-sm" id="upstream-model-picker" style="display:none" title="选择获取到的模型" aria-label="选择获取到的模型" aria-haspopup="listbox" aria-expanded="false">${icon('chevron_down', 'w-3.5 h-3.5')}</button><div class="router-model-options" id="upstream-model-options" popover="auto" role="listbox" aria-label="获取到的模型"></div><button type="button" class="btn btn-outline btn-sm" id="fetch-models">获取名称</button></div></div>
        <div class="form-group"><label class="form-label">能力标签（纯元数据）</label><div class="flex gap-4 flex-wrap">${['text', 'tools', 'vision'].map((capability) => `<label class="flex items-center gap-2 text-sm"><input type="checkbox" class="checkbox model-cap" value="${capability}" ${model.capabilities.includes(capability) ? 'checked' : ''}>${capability}</label>`).join('')}</div></div>
        <label class="flex items-center gap-2 text-sm"><input type="checkbox" class="checkbox" id="model-enabled" ${model.enabled ? 'checked' : ''}>启用 Model</label>`,
      footerHtml: `<button class="btn btn-outline btn-sm dialog-close-btn">取消</button><button class="btn btn-primary btn-sm" id="model-confirm">保存到草稿</button>`,
      onMount: (dialog, close) => {
        const endpointSelect = dialog.querySelector<HTMLSelectElement>('#model-endpoint')!;
        const upstreamInput = dialog.querySelector<HTMLInputElement>('#model-upstream')!;
        const upstreamPicker = dialog.querySelector<HTMLButtonElement>('#upstream-model-picker')!;
        const upstreamOptions = dialog.querySelector<HTMLElement>('#upstream-model-options')!;
        const fetchButton = dialog.querySelector<HTMLButtonElement>('#fetch-models')!;
        let fetchedModels: string[] = [];
        const closeUpstreamOptions = () => {
          if (upstreamOptions.matches(':popover-open')) upstreamOptions.hidePopover();
        };
        const positionUpstreamOptions = () => {
          const inputRect = upstreamInput.getBoundingClientRect();
          const pickerRect = upstreamPicker.getBoundingClientRect();
          const viewportPadding = 8;
          const gap = 4;
          const desiredHeight = Math.min(320, fetchedModels.length * 34 + 8);
          const spaceBelow = window.innerHeight - pickerRect.bottom - gap - viewportPadding;
          const spaceAbove = pickerRect.top - gap - viewportPadding;
          const openBelow = spaceBelow >= Math.min(desiredHeight, 192) || spaceBelow >= spaceAbove;
          const availableHeight = Math.max(96, openBelow ? spaceBelow : spaceAbove);
          const menuHeight = Math.min(desiredHeight, availableHeight);
          const menuWidth = Math.min(
            Math.max(240, inputRect.width + pickerRect.width + gap),
            window.innerWidth - viewportPadding * 2,
          );
          const left = Math.min(
            Math.max(viewportPadding, inputRect.left),
            window.innerWidth - menuWidth - viewportPadding,
          );
          upstreamOptions.style.left = `${left}px`;
          upstreamOptions.style.top = `${openBelow ? pickerRect.bottom + gap : pickerRect.top - menuHeight - gap}px`;
          upstreamOptions.style.width = `${menuWidth}px`;
          upstreamOptions.style.maxHeight = `${availableHeight}px`;
        };
        upstreamPicker.addEventListener('click', () => {
          if (upstreamOptions.matches(':popover-open')) {
            closeUpstreamOptions();
            return;
          }
          positionUpstreamOptions();
          upstreamOptions.showPopover();
        });
        upstreamOptions.addEventListener('toggle', () => {
          upstreamPicker.setAttribute('aria-expanded', String(upstreamOptions.matches(':popover-open')));
        });
        endpointSelect.addEventListener('change', () => {
          fetchedModels = [];
          closeUpstreamOptions();
          upstreamPicker.style.display = 'none';
          upstreamOptions.innerHTML = '';
        });
        upstreamOptions.addEventListener('click', (event) => {
          const option = (event.target as HTMLElement).closest<HTMLElement>('[data-model-index]');
          if (!option) return;
          const selected = fetchedModels[Number(option.dataset.modelIndex)];
          if (selected !== undefined) upstreamInput.value = selected;
          closeUpstreamOptions();
        });
        fetchButton.addEventListener('click', async () => {
          if (fetchButton.disabled) return;
          const endpointId = endpointSelect.value;
          fetchButton.disabled = true;
          fetchButton.setAttribute('aria-busy', 'true');
          fetchButton.innerHTML = '<span class="spinner inline-block" style="width:0.875rem;height:0.875rem"></span> 获取中...';
          try {
            if (!(await saveDraft(false, false))) return;
            const response = await api.listUpstreamModels(endpointId);
            if (response.error) {
              toast.error(response.error);
              return;
            }
            fetchedModels = [...response.models];
            upstreamOptions.innerHTML = fetchedModels.map((name, index) => `<button type="button" class="dropdown-item font-mono" role="option" data-model-index="${index}">${escapeHtml(name)}</button>`).join('');
            closeUpstreamOptions();
            upstreamPicker.style.display = fetchedModels.length ? 'inline-flex' : 'none';
            toast.success(`已获取 ${response.models.length} 个模型`);
          } catch (err) {
            toast.error('获取模型列表失败: ' + errorText(err));
          } finally {
            fetchButton.disabled = false;
            fetchButton.removeAttribute('aria-busy');
            fetchButton.textContent = '获取名称';
          }
        });
        dialog.querySelector('#model-confirm')?.addEventListener('click', () => {
          model.displayName = dialog.querySelector<HTMLInputElement>('#model-name')!.value;
          model.endpointId = dialog.querySelector<HTMLSelectElement>('#model-endpoint')!.value;
          model.upstreamModel = dialog.querySelector<HTMLInputElement>('#model-upstream')!.value;
          model.enabled = dialog.querySelector<HTMLInputElement>('#model-enabled')!.checked;
          model.capabilities = Array.from(dialog.querySelectorAll<HTMLInputElement>('.model-cap:checked')).map((item) => item.value);
          const index = draft.models.findIndex((item) => item.id === model.id);
          if (index >= 0) draft.models[index] = model;
          else draft.models.push(model);
          close();
          render();
        });
      },
    });
  }

  function editGroup(platform = '', groupId = '') {
    const existing = draft.groupOverrides.find((item) => item.platform === platform && item.groupId === groupId);
    const group = existing
      ? structuredClone(existing)
      : create(GroupModelOverrideSchema, { platform: platform || 'onebot', groupId, bindings: [] });
    void openDialog({
      title: '群级模型配置',
      description: '仅保存相对于全局配置的 diff。',
      maxWidth: '38rem',
      bodyHtml: `
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:.75rem"><div class="form-group"><label class="form-label">平台</label><input class="input" id="group-platform" value="${escapeHtml(group.platform)}"></div><div class="form-group"><label class="form-label">群 ID</label><input class="input font-mono" id="group-id" value="${escapeHtml(group.groupId)}"></div></div>
        ${workloads.map((workload) => `<div class="card p-3"><div class="flex items-center justify-between gap-2 mb-2"><strong class="text-sm">${workload.label}</strong>${workload.applied ? '' : '<span class="badge badge-warning">预留，当前不生效</span>'}</div><select class="select group-binding" data-workload="${workload.value}">${bindingOptions(bindingFor(group.bindings, workload.value), true, '继承全局', workload.value !== ModelWorkload.REFLECTION)}</select></div>`).join('')}`,
      footerHtml: `<button class="btn btn-outline btn-sm dialog-close-btn">取消</button><button class="btn btn-primary btn-sm" id="group-confirm">保存到草稿</button>`,
      onMount: (dialog, close) => {
        dialog.querySelector('#group-confirm')?.addEventListener('click', () => {
          const oldPlatform = group.platform;
          const oldGroupId = group.groupId;
          group.platform = dialog.querySelector<HTMLInputElement>('#group-platform')!.value.trim().toLowerCase();
          group.groupId = dialog.querySelector<HTMLInputElement>('#group-id')!.value.trim();
          group.bindings = [];
          dialog.querySelectorAll<HTMLSelectElement>('.group-binding').forEach((select) => {
            setBinding(group.bindings, Number(select.dataset.workload) as ModelWorkload, select.value, true);
          });
          const index = draft.groupOverrides.findIndex((item) => item.platform === oldPlatform && item.groupId === oldGroupId);
          if (index >= 0) draft.groupOverrides[index] = group;
          else draft.groupOverrides.push(group);
          close();
          render();
        });
      },
    });
  }

  function setBinding(bindings: ModelBinding[], workload: ModelWorkload, value: string, allowInherit: boolean) {
    const index = bindings.findIndex((binding) => binding.workload === workload);
    if (value === 'inherit' && allowInherit) {
      if (index >= 0) bindings.splice(index, 1);
      return;
    }
    const binding = create(ModelBindingSchema, {
      workload,
      mode: value === 'disabled' ? ModelBindingMode.DISABLED : ModelBindingMode.MODEL,
      modelId: value.startsWith('model:') ? value.slice(6) : bindings[index]?.modelId || '',
    });
    if (index >= 0) bindings[index] = binding;
    else bindings.push(binding);
  }

  function modelIsReferenced(modelId: string): boolean {
    return [...draft.globalBindings, ...draft.groupOverrides.flatMap((group) => group.bindings)]
      .some((binding) => binding.modelId === modelId);
  }

  async function testModel(modelId: string) {
    if (testingModelId) return;
    testingModelId = modelId;
    updateModelTestButtons();
    try {
      if (!(await saveDraft(false, false))) return;
      const response = await api.testModel(modelId);
      if (!response.success) {
        toast.error(response.error || '测试失败');
        return;
      }
      void openDialog({
        title: '模型测试成功',
        description: `耗时 ${response.durationMs} ms · 提示词：Introduce yourself in one sentence.`,
        bodyHtml: `<div class="card p-3 whitespace-pre-wrap select-text">${escapeHtml(response.content)}</div>`,
        footerHtml: `<button class="btn btn-primary btn-sm dialog-close-btn">关闭</button>`,
      });
    } catch (err) {
      toast.error('测试失败: ' + errorText(err));
    } finally {
      testingModelId = '';
      updateModelTestButtons();
    }
  }

  function updateModelTestButtons() {
    container.querySelectorAll<HTMLButtonElement>('[data-action="test-model"]').forEach((button) => {
      const testing = testingModelId === button.dataset.id;
      button.disabled = testingModelId !== '';
      button.setAttribute('aria-busy', String(testing));
      button.innerHTML = testing
        ? '<span class="spinner inline-block" style="width:0.875rem;height:0.875rem"></span> 测试中...'
        : `${icon('play', 'w-3.5 h-3.5')} 测试`;
    });
  }

  function openRequestedGroup() {
    const query = window.location.hash.split('?')[1] || '';
    const params = new URLSearchParams(query);
    const platform = params.get('platform') || '';
    const groupId = params.get('group_id') || '';
    if (!platform || !groupId) return;
    tab = 'groups';
    render();
    editGroup(platform.toLowerCase(), groupId);
  }

  function errorText(error: unknown): string {
    return error instanceof Error ? error.message : String(error);
  }

  void load();
  return () => {
    unmounted = true;
  };
}
