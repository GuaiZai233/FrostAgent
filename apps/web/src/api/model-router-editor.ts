import { create, toJsonString } from '@bufbuild/protobuf';
import { ModelRouterConfigurationSchema, type ModelRouterConfiguration } from '@frostagent/proto';
import { createInstanceAPI } from './client';
import { instanceState } from '../instance-state';

type RouterAPI = Pick<ReturnType<typeof createInstanceAPI>,
  'getModelRouterState' | 'saveModelRouterDraft' | 'publishModelRouter' | 'discardModelRouterDraft'
  | 'resetSharedCatalogDraft' | 'setDraftEndpointSecret' | 'clearDraftEndpointSecret' | 'listUpstreamModels' | 'testModel'>;

const emptyConfiguration = () => create(ModelRouterConfigurationSchema, { version: 1 });
const fingerprint = (config: ModelRouterConfiguration) => toJsonString(ModelRouterConfigurationSchema, config);

function retainWithUniqueName<T extends { displayName: string }>(items: T[], original: T) {
  const retained = structuredClone(original);
  const names = new Set(items.map((item) => item.displayName.trim().toLowerCase()));
  for (let suffix = 1; names.has(retained.displayName.trim().toLowerCase()); suffix++) {
    retained.displayName = `${original.displayName} (待移除 ${suffix})`;
  }
  items.push(retained);
}

export class ModelRouterEditorError extends Error {
  constructor(message: string, readonly active: ModelRouterConfiguration,
    readonly draft: ModelRouterConfiguration, readonly hasDraftSecretChanges: boolean) {
    super(message);
  }
}

// One editor owns two independently versioned scopes. Catalog calls always use
// the root service; routing calls stay bound to the instance selected on mount.
function catalogDraftOwner() {
  const key = `frostagent:model-catalog-draft:${instanceState.selected?.id ?? 'global'}`;
  try {
    const existing = sessionStorage.getItem(key);
    if (existing) return existing;
    const owner = crypto.randomUUID();
    sessionStorage.setItem(key, owner);
    return owner;
  } catch {
    return crypto.randomUUID();
  }
}

export function createModelRouterEditorAPI(
  catalog: RouterAPI = createInstanceAPI({ globalModelCatalog: true, catalogDraftOwner: catalogDraftOwner() }),
  routes: RouterAPI | undefined = instanceState.selected ? createInstanceAPI() : undefined,
) {
  let catalogActive = emptyConfiguration();
  let routesActive = emptyConfiguration();
  let pending = emptyConfiguration();
  let secretDirty = false;
  let ready = false;
  const merge = () => structuredClone({ ...routesActive, endpoints: catalogActive.endpoints, models: catalogActive.models });
  const catalogConfiguration = () => structuredClone({ ...catalogActive, endpoints: pending.endpoints, models: pending.models });
  const routeConfiguration = () => structuredClone({ ...routesActive, globalBindings: pending.globalBindings, groupOverrides: pending.groupOverrides });
  const catalogChanged = () => fingerprint(catalogConfiguration()) !== fingerprint(catalogActive);
  const routesChanged = () => routes && fingerprint(routeConfiguration()) !== fingerprint(routesActive);
  const assertReady = () => { if (!ready) throw new Error('模型配置尚未成功加载，请刷新后重试'); };
  const snapshotError = (error: unknown, completed: string[]) => new ModelRouterEditorError(
    `${completed.length ? `${completed.join('、')}；` : ''}${error instanceof Error ? error.message : String(error)}`,
    merge(), structuredClone(pending), secretDirty,
  );

  async function syncCatalog(configuration = catalogConfiguration(), preservePending = false) {
    const response = await catalog.saveModelRouterDraft(configuration);
    if (!response.success || !response.draft) throw new Error(response.error || '同步共享供应商失败');
    if (!preservePending) {
      pending.endpoints = structuredClone(response.draft.endpoints);
      pending.models = structuredClone(response.draft.models);
    }
  }

  return {
    hasInstance: !!routes,
    async getModelRouterState() {
      ready = false;
      const [catalogState, routeState] = await Promise.all([
        catalog.getModelRouterState(), routes?.getModelRouterState(),
      ]);
      catalogActive = structuredClone(catalogState.active ?? emptyConfiguration());
      routesActive = structuredClone(routeState?.active ?? emptyConfiguration());
      pending = merge();
      pending.endpoints = structuredClone(catalogState.draft?.endpoints ?? catalogActive.endpoints);
      pending.models = structuredClone(catalogState.draft?.models ?? catalogActive.models);
      secretDirty = catalogState.hasDraftSecretChanges;
      const staleCatalogDraft = catalogState.draft && catalogState.draft.revision !== catalogActive.revision;
      const loadError = [catalogState.loadError, routeState?.loadError,
        staleCatalogDraft ? '共享供应商目录已更新，当前草稿基于旧版本。请点击“重置共享草稿”后重新编辑' : '',
      ].filter(Boolean).join('；');
      ready = !loadError;
      return { active: merge(), draft: structuredClone(pending), hasDraftSecretChanges: secretDirty, loadError };
    },
    async saveModelRouterDraft(configuration: ModelRouterConfiguration, syncForPreview = false) {
      assertReady();
      pending = structuredClone(configuration);
      // New routes may refer to models which are still only in the catalog
      // draft. Save routes after catalog publication, when those IDs exist.
      if (syncForPreview) await syncCatalog();
      return { success: true, draft: structuredClone(pending), error: '' };
    },
    async publishModelRouter() {
      assertReady();
      const completed: string[] = [];
      const publishCatalog = async (configuration = catalogConfiguration(), preservePending = false) => {
        if (!catalogChanged() && !secretDirty) return;
        await syncCatalog(configuration, preservePending);
        const response = await catalog.publishModelRouter();
        if (!response.success || !response.active) throw new Error(response.error || '发布共享供应商失败');
        catalogActive = structuredClone(response.active);
        if (!preservePending) {
          pending.endpoints = structuredClone(catalogActive.endpoints);
          pending.models = structuredClone(catalogActive.models);
        }
        secretDirty = false;
        completed.push('共享供应商已发布');
      };
      const publishRoutes = async () => {
        if (!routes || !routesChanged()) return;
        const configuration = routeConfiguration();
        const response = await routes.saveModelRouterDraft(configuration);
        if (!response.success || !response.draft) throw new Error(response.error || '同步实例路由失败');
        const published = await routes.publishModelRouter(configuration);
        if (!published.success || !published.active) throw new Error(published.error || '发布实例路由失败');
        routesActive = structuredClone(published.active);
        pending.globalBindings = structuredClone(routesActive.globalBindings);
        pending.groupOverrides = structuredClone(routesActive.groupOverrides);
        pending.revision = routesActive.revision;
        completed.push('当前实例路由已发布');
      };
      try {
        const references = (config: ModelRouterConfiguration) => [...config.globalBindings,
          ...config.groupOverrides.flatMap((group) => group.bindings)].map((binding) => binding.modelId).filter(Boolean);
        const needsUnbind = routes && references(routesActive).some((id) => !pending.models.some((model) => model.id === id));
        if (needsUnbind) {
          if (references(pending).some((id) => !catalogActive.models.some((model) => model.id === id))) {
            // Make replacement models available before moving bindings, while
            // retaining removed models until no instance route needs them.
            const bridge = catalogConfiguration();
            for (const model of catalogActive.models) {
              if (bridge.models.some((item) => item.id === model.id)) continue;
              retainWithUniqueName(bridge.models, model);
              const endpoint = catalogActive.endpoints.find((item) => item.id === model.endpointId);
              if (endpoint && !bridge.endpoints.some((item) => item.id === endpoint.id)) {
                retainWithUniqueName(bridge.endpoints, endpoint);
              }
            }
            await publishCatalog(bridge, true);
          }
          await publishRoutes();
        }
        await publishCatalog();
        await publishRoutes();
        pending = merge();
        return { success: true, active: merge(), error: '' };
      } catch (error) {
        throw snapshotError(error, completed);
      }
    },
    async discardModelRouterDraft(configuration: ModelRouterConfiguration) {
      assertReady();
      pending = structuredClone(configuration);
      const completed: string[] = [];
      try {
        if (catalogChanged() || secretDirty) {
          const discardedCatalog = await catalog.discardModelRouterDraft();
          if (!discardedCatalog) throw new Error('放弃共享供应商草稿失败');
          catalogActive = structuredClone(discardedCatalog);
          completed.push('共享供应商草稿已放弃');
        }
        pending.endpoints = structuredClone(catalogActive.endpoints);
        pending.models = structuredClone(catalogActive.models);
        secretDirty = false;
        if (routes) {
          const discardedRoutes = await routes.discardModelRouterDraft();
          if (!discardedRoutes) throw new Error('放弃实例路由草稿失败');
          routesActive = structuredClone(discardedRoutes);
        }
        pending = merge();
        return merge();
      } catch (error) {
        throw snapshotError(error, completed);
      }
    },
    async resetSharedCatalogDraft() {
      const discarded = await catalog.resetSharedCatalogDraft();
      if (!discarded) throw new Error('重置共享供应商草稿失败');
      catalogActive = structuredClone(discarded);
      pending.endpoints = structuredClone(discarded.endpoints);
      pending.models = structuredClone(discarded.models);
      secretDirty = false;
      return structuredClone(pending);
    },
    async setDraftEndpointSecret(endpointId: string, apiKey: string) {
      assertReady();
      const response = await catalog.setDraftEndpointSecret(endpointId, apiKey);
      if (response.success) secretDirty = true;
      return response;
    },
    async clearDraftEndpointSecret(endpointId: string) {
      assertReady();
      const response = await catalog.clearDraftEndpointSecret(endpointId);
      if (response.success) secretDirty = true;
      return response;
    },
    listUpstreamModels: catalog.listUpstreamModels,
    testModel: catalog.testModel,
  };
}
