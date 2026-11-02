import assert from 'node:assert/strict';
import { create } from '@bufbuild/protobuf';
import { ModelBindingMode, ModelRouterConfigurationSchema, ModelWorkload, type ModelRouterConfiguration } from '@frostagent/proto';
import { createModelRouterEditorAPI, ModelRouterEditorError } from '../src/api/model-router-editor';

const configuration = (revision: bigint, modelId = 'shared-model') => create(ModelRouterConfigurationSchema, {
  version: 1, revision,
  endpoints: [{ id: 'shared-provider', displayName: 'Shared', baseUrl: 'https://provider.invalid/v1', enabled: true }],
  models: [{ id: 'shared-model', displayName: 'Shared Model', endpointId: 'shared-provider', upstreamModel: 'test-model', enabled: true }],
  globalBindings: [{ workload: ModelWorkload.DIALOGUE, mode: ModelBindingMode.MODEL, modelId }],
});

function fixture(withInstance = true) {
  const calls: string[] = [];
  const catalogInitial = configuration(7n);
  catalogInitial.globalBindings = [];
  const state = {
    catalog: catalogInitial, routes: configuration(3n),
    catalogDraft: structuredClone(catalogInitial), routeDraft: configuration(3n),
    exposeCatalogDraft: false, catalogSecretDirty: false,
    failCatalog: false, failRoutes: false, failDiscardRoutes: false, failResetCatalog: false,
    failCatalogPublishNumber: 0, catalogPublishes: 0, replaceRouteDraft: false,
  };
  const makeAPI = (scope: 'catalog' | 'routes') => ({
    async getModelRouterState() {
      calls.push(`${scope}:load`);
      return { active: structuredClone(state[scope]),
        draft: scope === 'catalog' && state.exposeCatalogDraft ? structuredClone(state.catalogDraft) : undefined,
        loadError: '', hasDraftSecretChanges: scope === 'catalog' && state.exposeCatalogDraft && state.catalogSecretDirty };
    },
    async saveModelRouterDraft(config: ModelRouterConfiguration) {
      calls.push(`${scope}:save`);
      assert.equal(config.revision, state[scope].revision, `${scope} used the other scope's revision`);
      if (scope === 'catalog') {
        assert.deepEqual(config.globalBindings, [], 'instance routes leaked into catalog request');
        assert.deepEqual(config.groupOverrides, []);
        for (const items of [config.endpoints, config.models]) {
          const names = items.map((item) => item.displayName.trim().toLowerCase());
          assert.equal(new Set(names).size, names.length, 'catalog display names must be unique');
        }
        state.catalogDraft = structuredClone(config);
      } else {
        assert.ok(config.globalBindings.every((binding) => !binding.modelId || state.catalog.models.some((model) => model.id === binding.modelId)), 'route saved before its new model was published');
        state.routeDraft = structuredClone(config);
        if (state.replaceRouteDraft) state.routeDraft.groupOverrides = [{
          $typeName: 'frostagent.v1.GroupModelOverride', platform: 'qq', groupId: 'synthetic-other-group', bindings: [],
        }];
      }
      return { success: true, draft: structuredClone(scope === 'routes' ? state.routeDraft : config), error: '' };
    },
    async publishModelRouter(expected?: ModelRouterConfiguration) {
      calls.push(`${scope}:publish`);
      if (scope === 'routes') {
        assert.ok(expected, 'instance publication did not include its expected configuration');
        if (JSON.stringify(expected.groupOverrides) !== JSON.stringify(state.routeDraft.groupOverrides)) {
          return { success: false, error: 'route draft replaced by another page' };
        }
        assert.deepEqual(expected.globalBindings, state.routeDraft.globalBindings);
        assert.equal(expected.revision, state.routes.revision);
      } else assert.equal(expected, undefined, 'catalog publication adopted the route contract');
      if (scope === 'catalog') {
        state.catalogPublishes++;
        if (state.catalogPublishes === state.failCatalogPublishNumber) return { success: false, error: 'catalog unavailable' };
      }
      if (scope === 'catalog' ? state.failCatalog : state.failRoutes) return { success: false, error: `${scope} unavailable` };
      if (scope === 'catalog') {
        assert.ok(state.routes.globalBindings.every((binding) => !binding.modelId || state.catalogDraft.models.some((model) => model.id === binding.modelId)), 'referenced model deleted before unbinding');
        state.catalog = structuredClone(state.catalogDraft);
      } else state.routes = structuredClone(state.routeDraft);
      state[scope].revision++;
      return { success: true, active: structuredClone(state[scope]), error: '' };
    },
    async discardModelRouterDraft() {
      calls.push(`${scope}:discard`);
      if (scope === 'routes' && state.failDiscardRoutes) throw new Error('route discard unavailable');
      if (scope === 'routes') state.routeDraft = structuredClone(state.routes);
      return structuredClone(state[scope]);
    },
    async resetSharedCatalogDraft() {
      calls.push(`${scope}:reset`);
      if (state.failResetCatalog) throw new Error('catalog reset unavailable');
      state.catalogDraft = structuredClone(state.catalog);
      state.catalogSecretDirty = false;
      return structuredClone(state.catalog);
    },
    async setDraftEndpointSecret() { calls.push(`${scope}:secret`); return { success: true, configured: true, error: '' }; },
    async clearDraftEndpointSecret() { calls.push(`${scope}:clear`); return { success: true, configured: false, error: '' }; },
    async listUpstreamModels() { calls.push(`${scope}:list`); return { models: ['test-model'], error: '' }; },
    async testModel() { calls.push(`${scope}:test`); return { success: true, durationMs: 1n, content: 'test', error: '' }; },
  });
  type API = NonNullable<Parameters<typeof createModelRouterEditorAPI>[0]>;
  const editor = createModelRouterEditorAPI(makeAPI('catalog') as API, withInstance ? makeAPI('routes') as API : undefined);
  return { calls, state, editor };
}

{
  const { editor, state } = fixture();
  const draft = (await editor.getModelRouterState()).active;
  draft.globalBindings = [];
  await editor.saveModelRouterDraft(draft);
  state.replaceRouteDraft = true;
  await assert.rejects(editor.publishModelRouter(), /route draft replaced by another page/);
  assert.equal(state.routes.globalBindings[0].modelId, 'shared-model', 'conflicting draft was published');
  state.replaceRouteDraft = false;
  await editor.publishModelRouter();
  assert.deepEqual(state.routes.globalBindings, [], 'retry lost the original route edit');
}

{
  const { editor, state, calls } = fixture();
  const draft = (await editor.getModelRouterState()).active;
  draft.models.push({ ...draft.models[0], id: 'new-model', displayName: 'New model' });
  draft.globalBindings[0].modelId = 'new-model';
  await editor.saveModelRouterDraft(draft);
  assert.equal(state.routes.globalBindings[0].modelId, 'shared-model');
  const result = await editor.publishModelRouter();
  assert.equal(result.active.globalBindings[0].modelId, 'new-model');
  assert.deepEqual(calls.slice(2), ['catalog:save', 'catalog:publish', 'routes:save', 'routes:publish']);
}

{
  const { editor, calls } = fixture();
  const draft = (await editor.getModelRouterState()).active;
  draft.globalBindings = [];
  await editor.saveModelRouterDraft(draft);
  await editor.publishModelRouter();
  assert.deepEqual(calls.slice(2), ['routes:save', 'routes:publish'], 'route-only edits must not overwrite the shared catalog');
}

{
  // Another instance has already staged a new URL and key in the shared
  // catalog. This editor receives only its own view of the active catalog.
  const { editor, state, calls } = fixture();
  state.catalogDraft.endpoints[0].baseUrl = 'https://pending.invalid/v1';
  const draft = (await editor.getModelRouterState()).active;
  draft.globalBindings = [];
  await editor.saveModelRouterDraft(draft);
  await editor.publishModelRouter();
  assert.deepEqual(calls.slice(2), ['routes:save', 'routes:publish']);
  assert.equal(state.catalog.endpoints[0].baseUrl, 'https://provider.invalid/v1');
  assert.equal(state.catalogDraft.endpoints[0].baseUrl, 'https://pending.invalid/v1');
}

{
  // Reopening the page that owns a staged key must restore the matching URL.
  const { editor, state } = fixture();
  state.exposeCatalogDraft = true;
  state.catalogSecretDirty = true;
  state.catalogDraft.endpoints[0].baseUrl = 'https://pending.invalid/v1';
  const loaded = await editor.getModelRouterState();
  assert.equal(loaded.active.endpoints[0].baseUrl, 'https://provider.invalid/v1');
  assert.equal(loaded.draft.endpoints[0].baseUrl, 'https://pending.invalid/v1');
  await editor.saveModelRouterDraft(loaded.draft);
  await editor.publishModelRouter();
  assert.equal(state.catalog.endpoints[0].baseUrl, 'https://pending.invalid/v1');
}

{
  // A restore can update the catalog while this page owns unpublished edits.
  // Keep those edits visible, block publication, and permit explicit reset.
  const { editor, state, calls } = fixture();
  state.exposeCatalogDraft = true;
  state.catalogDraft.endpoints[0].baseUrl = 'https://pending.invalid/v1';
  state.catalog.revision++;
  const loaded = await editor.getModelRouterState();
  assert.match(loaded.loadError, /当前草稿基于旧版本/);
  assert.equal(loaded.draft.endpoints[0].baseUrl, 'https://pending.invalid/v1');
  await assert.rejects(editor.saveModelRouterDraft(loaded.draft), /尚未成功加载/);
  await assert.rejects(editor.publishModelRouter(), /尚未成功加载/);
  assert.deepEqual(calls, ['catalog:load', 'routes:load']);
  assert.match(loaded.loadError, /放弃改动/);
  await editor.discardChanges(loaded.draft);
  assert.deepEqual(calls.slice(2), ['catalog:reset', 'routes:discard']);
  const recovered = await editor.getModelRouterState();
  assert.equal(recovered.loadError, '');
  assert.equal(recovered.draft.endpoints[0].baseUrl, 'https://provider.invalid/v1');
}

{
  const { editor, state, calls } = fixture();
  const draft = (await editor.getModelRouterState()).active;
  draft.endpoints[0].displayName = 'Updated shared provider';
  draft.globalBindings = [];
  await editor.saveModelRouterDraft(draft);
  state.failRoutes = true;
  let failure: ModelRouterEditorError | undefined;
  try { await editor.publishModelRouter(); } catch (error) { failure = error as ModelRouterEditorError; }
  assert.ok(failure instanceof ModelRouterEditorError);
  assert.match(failure.message, /共享供应商已发布.*routes unavailable/);
  assert.equal(failure.active.endpoints[0].displayName, 'Updated shared provider');
  assert.equal(failure.active.globalBindings[0].modelId, 'shared-model');
  assert.deepEqual(failure.draft.globalBindings, [], 'failed route edits were discarded');
  state.failRoutes = false;
  await editor.saveModelRouterDraft(failure.draft);
  await editor.publishModelRouter();
  assert.equal(calls.filter((call) => call === 'catalog:publish').length, 1, 'retry republished the completed scope');
}

{
  const { editor, state, calls } = fixture();
  const draft = (await editor.getModelRouterState()).active;
  draft.globalBindings = [];
  draft.models = [];
  await editor.saveModelRouterDraft(draft);
  state.failCatalog = true;
  await assert.rejects(editor.publishModelRouter(), (error: unknown) => {
    assert.ok(error instanceof ModelRouterEditorError);
    assert.match(error.message, /当前实例路由已发布.*catalog unavailable/);
    assert.deepEqual(error.active.globalBindings, []);
    assert.equal(error.active.models.length, 1);
    assert.deepEqual(error.draft.models, []);
    return true;
  });
  assert.deepEqual(calls.slice(2), ['routes:save', 'routes:publish', 'catalog:save', 'catalog:publish']);
  state.failCatalog = false;
  await editor.publishModelRouter();
  assert.deepEqual(state.catalog.models, []);
}

{
  const { editor, calls } = fixture(false);
  await editor.getModelRouterState();
  assert.equal(editor.hasInstance, false);
  await editor.setDraftEndpointSecret('shared-provider', 'synthetic-key');
  await editor.listUpstreamModels('shared-provider');
  await editor.testModel('shared-model');
  await editor.publishModelRouter();
  assert.ok(calls.every((call) => call.startsWith('catalog:')), 'zero-instance editor sent instance requests');
  assert.ok(calls.includes('catalog:publish'), 'secret-only edits were not published');
}

{
  const { editor, state } = fixture();
  const draft = (await editor.getModelRouterState()).active;
  draft.endpoints[0].displayName = 'Unsaved';
  draft.globalBindings = [];
  state.failDiscardRoutes = true;
  await assert.rejects(editor.discardChanges(draft), (error: unknown) => {
    assert.ok(error instanceof ModelRouterEditorError);
    assert.equal(error.draft.endpoints[0].displayName, 'Shared');
    assert.deepEqual(error.draft.globalBindings, []);
    assert.match(error.message, /共享供应商改动已放弃.*route discard unavailable/);
    return true;
  });
  state.failDiscardRoutes = false;
  const restored = await editor.discardChanges(draft);
  assert.equal(restored.globalBindings[0].modelId, 'shared-model');
}

{
  const { editor, state, calls } = fixture();
  const loaded = await editor.getModelRouterState();
  const draft = structuredClone(loaded.draft);
  draft.endpoints[0].displayName = 'Unsaved provider';
  draft.globalBindings = [];
  state.catalogDraft.endpoints[0].baseUrl = 'https://other-page.invalid/v1';
  state.catalogSecretDirty = true;
  state.routeDraft.globalBindings = [];
  state.failResetCatalog = true;
  await assert.rejects(editor.discardChanges(draft), (error: unknown) => {
    assert.ok(error instanceof ModelRouterEditorError);
    assert.equal(error.draft.endpoints[0].displayName, 'Unsaved provider');
    assert.deepEqual(error.draft.globalBindings, []);
    assert.match(error.message, /catalog reset unavailable/);
    return true;
  });
  assert.deepEqual(calls.slice(2), ['catalog:reset'], 'failed catalog reset must not discard route edits');
  state.failResetCatalog = false;
  const restored = await editor.discardChanges(draft);
  assert.equal(restored.endpoints[0].displayName, 'Shared');
  assert.equal(restored.globalBindings[0].modelId, 'shared-model');
  assert.deepEqual(state.catalogDraft, state.catalog, 'all shared catalog edits must be reset');
  assert.deepEqual(state.routeDraft, state.routes, 'server-side instance draft must also be discarded');
  assert.equal(state.catalogSecretDirty, false);
}

{
  const { editor, calls } = fixture(false);
  const loaded = await editor.getModelRouterState();
  await editor.discardChanges(loaded.draft);
  assert.deepEqual(calls, ['catalog:load', 'catalog:reset'], 'discard without an instance must stay catalog-only');
}

console.log('PASS: unified model editor preserves scope, publication order, and failed edits');

for (const failureStage of ['none', 'add', 'route', 'remove']) {
  const { editor, state, calls } = fixture();
  const draft = (await editor.getModelRouterState()).active;
  draft.endpoints = [{ ...draft.endpoints[0], id: 'new-provider' }];
  draft.models = [{ ...draft.models[0], id: 'new-model', endpointId: 'new-provider' }];
  draft.globalBindings[0].modelId = 'new-model';
  await editor.saveModelRouterDraft(draft);
  state.failCatalogPublishNumber = failureStage === 'add' ? 1 : failureStage === 'remove' ? 2 : 0;
  state.failRoutes = failureStage === 'route';
  if (failureStage !== 'none') {
    let failure: ModelRouterEditorError | undefined;
    try { await editor.publishModelRouter(); } catch (error) { failure = error as ModelRouterEditorError; }
    assert.ok(failure instanceof ModelRouterEditorError, `${failureStage}: missing failure`);
    assert.deepEqual(failure.draft.models.map((model) => model.id), ['new-model'], 'intermediate catalog overwrote intended deletions');
    assert.equal(failure.draft.globalBindings[0].modelId, 'new-model');
    assert.equal(failure.active.globalBindings[0].modelId, failureStage === 'remove' ? 'new-model' : 'shared-model');
    assert.equal(failure.active.models.length, failureStage === 'add' ? 1 : 2);
    state.failCatalogPublishNumber = 0;
    state.failRoutes = false;
    await editor.saveModelRouterDraft(failure.draft);
  }
  await editor.publishModelRouter();
  assert.deepEqual(state.catalog.models.map((model) => model.id), ['new-model']);
  assert.deepEqual(state.catalog.endpoints.map((endpoint) => endpoint.id), ['new-provider']);
  assert.equal(state.catalog.endpoints[0].displayName, 'Shared');
  assert.equal(state.catalog.models[0].displayName, 'Shared Model');
  assert.equal(state.routes.globalBindings[0].modelId, 'new-model');
  if (failureStage === 'none') assert.deepEqual(calls.slice(2), [
    'catalog:save', 'catalog:publish', 'routes:save', 'routes:publish', 'catalog:save', 'catalog:publish',
  ]);
}

console.log('PASS: replacing referenced models publishes once from the UI and retries every failed phase');

{
  const { editor, state } = fixture();
  const draft = (await editor.getModelRouterState()).active;
  draft.endpoints[0].baseUrl = 'https://replacement.invalid/v1';
  draft.models = [{ ...draft.models[0], id: 'new-model' }];
  draft.globalBindings[0].modelId = 'new-model';
  await editor.saveModelRouterDraft(draft);
  state.failRoutes = true;
  await assert.rejects(editor.publishModelRouter(), (error: unknown) => {
    assert.ok(error instanceof ModelRouterEditorError);
    assert.equal(error.active.endpoints[0].baseUrl, 'https://replacement.invalid/v1', 'published global edits must be reflected even when instance publication fails');
    assert.equal(error.active.globalBindings[0].modelId, 'shared-model');
    assert.equal(error.draft.globalBindings[0].modelId, 'new-model');
    return true;
  });
  state.failRoutes = false;
  await editor.publishModelRouter();
  assert.equal(state.routes.globalBindings[0].modelId, 'new-model');
}
