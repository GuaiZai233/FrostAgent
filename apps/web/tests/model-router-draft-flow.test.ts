import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { create } from '@bufbuild/protobuf';
import { ModelRouterConfigurationSchema, type ModelRouterConfiguration } from '@frostagent/proto';
import { createModelRouterEditorAPI } from '../src/api/model-router-editor';
import { toast } from '../src/components/toast';
import { mountModelRouterPage } from '../src/pages/model-router';

const { JSDOM } = createRequire(resolve('package.json'))('jsdom');
const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'https://console.invalid/' });
const previousGlobals = new Map(['window', 'document'].map((key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
Object.defineProperty(globalThis, 'window', { configurable: true, value: dom.window });
Object.defineProperty(globalThis, 'document', { configurable: true, value: dom.window.document });
dom.window.HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
dom.window.HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
const errors: string[] = [];
const previousToast = { error: toast.error, success: toast.success };
toast.error = (message: string) => { errors.push(message); };
toast.success = () => undefined;
const settle = async () => { await delay(0); await delay(0); };
const click = (root: ParentNode, selector: string) => {
  const button = root.querySelector<HTMLButtonElement>(selector);
  assert.ok(button, `missing ${selector}`);
  button.click();
};

function fixture() {
  let active = create(ModelRouterConfigurationSchema, { version: 1 });
  let draft = structuredClone(active);
  const storedKeys = new Map<string, string>();
  const draftKeys = new Map<string, string>();
  let releaseSecret: (() => void) | undefined;
  let releaseSave: (() => void) | undefined;
  let releaseList: (() => void) | undefined;
  let holdSecret = false;
  let holdSave = false;
  let holdList = false;
  let failSecret = false;
  let failSave = false;
  let publishes = 0;
  let tests = 0;
  const catalog = {
    async getModelRouterState() {
      return { active: structuredClone(active), draft: structuredClone(draft), loadError: '', hasDraftSecretChanges: draftKeys.size > 0 };
    },
    async saveModelRouterDraft(configuration: ModelRouterConfiguration) {
      if (holdSave) await new Promise<void>((resolve) => { releaseSave = resolve; });
      if (failSave) return { success: false, error: 'synthetic draft save failure' };
      draft = structuredClone(configuration);
      return { success: true, draft: structuredClone(draft), error: '' };
    },
    async setDraftEndpointSecret(endpointId: string, value: string) {
      assert.ok(draft.endpoints.some((endpoint) => endpoint.id === endpointId), 'secret was staged before its endpoint');
      if (holdSecret) await new Promise<void>((resolve) => { releaseSecret = resolve; });
      if (failSecret) return { success: false, configured: false, error: 'synthetic secret save failure' };
      draftKeys.set(endpointId, value);
      draft.endpoints.find((endpoint) => endpoint.id === endpointId)!.apiKeyConfigured = true;
      return { success: true, configured: true, error: '' };
    },
    async listUpstreamModels(endpointId: string) {
      if (holdList) await new Promise<void>((resolve) => { releaseList = resolve; });
      assert.equal(draftKeys.get(endpointId), 'synthetic-draft-key');
      assert.equal(draft.endpoints.find((endpoint) => endpoint.id === endpointId)?.baseUrl, 'https://provider.invalid/v1');
      return { models: ['synthetic-model'], error: '' };
    },
    async inferModelCapabilities() { return { found: false, capabilities: [] }; },
    async testModel(modelId: string) {
      const model = draft.models.find((item) => item.id === modelId);
      assert.ok(model, 'preview could not see the unpublished model');
      assert.equal(draftKeys.get(model.endpointId), 'synthetic-draft-key', 'preview lost the unpublished key');
      tests++;
      return { success: true, content: 'Synthetic response', durationMs: 1n, error: '' };
    },
    async publishModelRouter() {
      active = structuredClone(draft);
      active.revision++;
      for (const [id, value] of draftKeys) storedKeys.set(id, value);
      draftKeys.clear();
      draft = structuredClone(active);
      publishes++;
      return { success: true, active: structuredClone(active), error: '' };
    },
  };
  type CatalogAPI = NonNullable<Parameters<typeof createModelRouterEditorAPI>[0]>;
  const editor = createModelRouterEditorAPI(catalog as CatalogAPI);
  const root = document.createElement('div');
  document.body.appendChild(root);
  let stop = mountModelRouterPage(root, editor);
  return {
    root, draftKeys, storedKeys,
    get draft() { return draft; },
    get publishes() { return publishes; },
    get tests() { return tests; },
    set holdSecret(value: boolean) { holdSecret = value; },
    set holdSave(value: boolean) { holdSave = value; },
    set holdList(value: boolean) { holdList = value; },
    set failSecret(value: boolean) { failSecret = value; },
    set failSave(value: boolean) { failSave = value; },
    releaseSecret() { assert.ok(releaseSecret); releaseSecret(); },
    releaseSave() { assert.ok(releaseSave); releaseSave(); },
    releaseList() { assert.ok(releaseList); releaseList(); },
    remount() { stop(); stop = mountModelRouterPage(root, createModelRouterEditorAPI(catalog as CatalogAPI)); },
    dispose() { stop(); root.remove(); document.querySelectorAll('dialog').forEach((dialog) => dialog.remove()); },
  };
}

function fillEndpoint(root: HTMLElement) {
  click(root, '#add-endpoint');
  const dialog = document.querySelector<HTMLDialogElement>('dialog')!;
  dialog.querySelector<HTMLInputElement>('#endpoint-name')!.value = 'Draft provider';
  dialog.querySelector<HTMLInputElement>('#endpoint-url')!.value = 'https://provider.invalid';
  dialog.querySelector<HTMLInputElement>('#endpoint-key')!.value = 'synthetic-draft-key';
  return dialog;
}

try {
  {
    const f = fixture();
    await settle();
    const endpointDialog = fillEndpoint(f.root);
    click(endpointDialog, '#endpoint-confirm');
    await settle();
    assert.equal(endpointDialog.isConnected, false);
    assert.equal(f.draftKeys.size, 1);
    assert.equal(f.storedKeys.size, 0, 'saving an endpoint must not publish its key');

    click(f.root, '[data-action="edit-endpoint"]');
    const editDialog = document.querySelector<HTMLDialogElement>('dialog')!;
    assert.equal(editDialog.querySelector<HTMLInputElement>('#endpoint-key')!.value, '', 'reopening must not reveal a staged key');
    editDialog.querySelector<HTMLInputElement>('#endpoint-url')!.value = 'https://provider.invalid/v1';
    click(editDialog, '#endpoint-confirm');
    await settle();
    assert.equal(f.draftKeys.size, 1, 'editing an unpublished endpoint with a blank key must preserve its staged key');

    click(f.root, '[data-router-tab="models"]');
    click(f.root, '#add-model');
    const modelDialog = document.querySelector<HTMLDialogElement>('dialog')!;
    f.holdList = true;
    click(modelDialog, '#fetch-models');
    await settle();
    assert.equal(modelDialog.querySelector<HTMLButtonElement>('#model-confirm')!.disabled, true, 'model saving must wait for model-list draft synchronization');
    f.releaseList();
    await settle();
    assert.equal(modelDialog.querySelector<HTMLButtonElement>('#model-confirm')!.disabled, false);
    modelDialog.querySelector<HTMLInputElement>('#model-upstream')!.value = 'synthetic-model';
    click(modelDialog, '#model-confirm');
    await settle();
    assert.equal(modelDialog.isConnected, false);
    assert.equal(f.draft.models.length, 1, 'Model Save must retain the model with its unpublished endpoint and key');
    assert.equal(f.publishes, 0, 'configuration was published before the explicit publish action');

    f.remount();
    await settle();
    click(f.root, '[data-router-tab="models"]');
    click(f.root, '[data-action="test-model"]');
    await settle();
    assert.equal(f.tests, 1);
    click(document.querySelector('dialog')!, '.dialog-close-btn');
    assert.equal(f.publishes, 0, 'testing must not publish the draft');
    click(f.root, '#router-publish');
    await settle();
    assert.equal(f.publishes, 1);
    assert.equal(f.storedKeys.get(f.draft.endpoints[0].id), 'synthetic-draft-key');
    assert.equal(f.draftKeys.size, 0);
    assert.doesNotMatch(f.root.textContent ?? '', /synthetic-draft-key/);
    f.dispose();
  }

  {
    const f = fixture();
    await settle();
    f.holdSecret = true;
    const dialog = fillEndpoint(f.root);
    click(dialog, '#endpoint-confirm');
    await settle();
    assert.equal(dialog.querySelector<HTMLInputElement>('#endpoint-key')!.disabled, true);
    assert.equal(dialog.querySelector<HTMLSelectElement>('#endpoint-key-storage')!.disabled, true);
    click(dialog, '.dialog-close-btn');
    assert.equal(dialog.isConnected, true, 'closing during key staging must wait for the save result');
    const cancel = new dom.window.Event('cancel', { cancelable: true });
    dialog.dispatchEvent(cancel);
    assert.equal(dialog.isConnected, true, 'Escape must not interrupt key staging');
    f.releaseSecret();
    await settle();
    assert.equal(dialog.isConnected, false);
    assert.equal(f.draftKeys.size, 1);
    assert.equal(f.publishes, 0);
    f.dispose();
  }

  {
    const f = fixture();
    await settle();
    click(fillEndpoint(f.root), '#endpoint-confirm');
    await settle();
    click(f.root, '[data-router-tab="models"]');
    click(f.root, '#add-model');
    const dialog = document.querySelector<HTMLDialogElement>('dialog')!;
    dialog.querySelector<HTMLInputElement>('#model-upstream')!.value = 'synthetic-model';
    f.holdSave = true;
    click(dialog, '#model-confirm');
    await settle();
    assert.equal(dialog.querySelector<HTMLInputElement>('#model-upstream')!.disabled, true);
    dialog.dispatchEvent(new dom.window.Event('cancel', { cancelable: true }));
    assert.equal(dialog.isConnected, true, 'Escape must wait for model draft synchronization');
    assert.equal(f.draft.models.length, 0);
    f.releaseSave();
    await settle();
    assert.equal(dialog.isConnected, false);
    assert.equal(f.draft.models.length, 1);
    assert.equal(f.draftKeys.size, 1);
    assert.equal(f.publishes, 0);
    f.dispose();
  }

  {
    const f = fixture();
    await settle();
    f.failSecret = true;
    const dialog = fillEndpoint(f.root);
    click(dialog, '#endpoint-confirm');
    await settle();
    assert.equal(dialog.isConnected, true);
    assert.equal(dialog.querySelector<HTMLInputElement>('#endpoint-key')!.value, 'synthetic-draft-key');
    assert.equal(dialog.querySelector<HTMLInputElement>('#endpoint-key')!.disabled, false);
    assert.equal(dialog.querySelector<HTMLButtonElement>('#endpoint-confirm')!.disabled, false);
    assert.match(errors.at(-1) ?? '', /synthetic secret save failure/);
    f.failSecret = false;
    click(dialog, '#endpoint-confirm');
    await settle();
    assert.equal(dialog.isConnected, false);
    assert.equal(f.draftKeys.size, 1);
    click(f.root, '[data-router-tab="models"]');
    click(f.root, '#add-model');
    const modelDialog = document.querySelector<HTMLDialogElement>('dialog')!;
    modelDialog.querySelector<HTMLInputElement>('#model-upstream')!.value = 'synthetic-model';
    f.failSave = true;
    click(modelDialog, '#model-confirm');
    await settle();
    assert.equal(modelDialog.isConnected, true, 'failed Model Save must remain retryable');
    assert.equal(modelDialog.querySelector<HTMLInputElement>('#model-upstream')!.disabled, false);
    assert.equal(f.draft.models.length, 0);
    f.failSave = false;
    click(modelDialog, '#model-confirm');
    await settle();
    assert.equal(modelDialog.isConnected, false);
    assert.equal(f.draft.models.length, 1);
    assert.equal(f.draftKeys.size, 1);
    f.dispose();
  }
  console.log('PASS: unpublished endpoint/key workflow survives model editing, previews, reopening, and save failures');
} finally {
  Object.assign(toast, previousToast);
  for (const [key, descriptor] of previousGlobals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor);
    else Reflect.deleteProperty(globalThis, key);
  }
  dom.window.close();
}
