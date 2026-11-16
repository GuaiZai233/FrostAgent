import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { create } from '@bufbuild/protobuf';
import { ModelRouterConfigurationSchema } from '@frostagent/proto';
import { mountModelRouterPage } from '../src/pages/model-router';

const { JSDOM } = createRequire(resolve('package.json'))('jsdom');
const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', { url: 'https://console.invalid/' });
const previous = new Map(['window', 'document'].map((key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
Object.defineProperty(globalThis, 'window', { configurable: true, value: dom.window });
Object.defineProperty(globalThis, 'document', { configurable: true, value: dom.window.document });
dom.window.HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
dom.window.HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
const click = (root: ParentNode, selector: string) => root.querySelector<HTMLButtonElement>(selector)!.click();
const settle = () => delay(0);
const root = document.querySelector<HTMLElement>('#root')!;
const active = create(ModelRouterConfigurationSchema, { version: 1, endpoints: [
  { id: 'legacy', displayName: 'Legacy', baseUrl: 'https://relay.invalid/v1', enabled: true },
] });
let saved = structuredClone(active);
let stop: (() => void) | undefined;
try {
  stop = mountModelRouterPage(root, {
    hasInstance: false,
    async getModelRouterState() { return { active, draft: active, hasDraftSecretChanges: false, loadError: '' }; },
    async saveModelRouterDraft(config: typeof active) { saved = structuredClone(config); return { success: true, draft: saved, error: '' }; },
  } as unknown as NonNullable<Parameters<typeof mountModelRouterPage>[1]>);
  await settle();
  click(root, '#add-endpoint');
  let dialog = document.querySelector('dialog')!;
  let format = dialog.querySelector<HTMLSelectElement>('#endpoint-api-format')!;
  assert.equal(format.options.length, 5);
  assert.equal(format.value, 'openai_chat_completions');
  assert.match(dialog.querySelector('#endpoint-url-hint')!.textContent!, /\/v1\/chat\/completions/);
  for (const [value, suffix] of [
    ['anthropic_messages', '/v1/messages'],
    ['openai_responses', '/v1/responses'],
    ['gemini_generate_content', '/v1beta/models/{model}:generateContent'],
  ]) {
    format.value = value;
    format.dispatchEvent(new dom.window.Event('change'));
    assert.ok(dialog.querySelector('#endpoint-url-hint')!.textContent!.includes(suffix));
  }
  format.value = 'custom';
  format.dispatchEvent(new dom.window.Event('change'));
  assert.equal(dialog.querySelector('#endpoint-url-label')!.textContent, '完整 URL');
  assert.match(dialog.querySelector('#endpoint-url-hint')!.textContent!, /不追加路径/);
  dialog.querySelector<HTMLInputElement>('#endpoint-name')!.value = 'Custom';
  const fullURL = 'https://relay.invalid/exact/?route=chat%2Ftest';
  dialog.querySelector<HTMLInputElement>('#endpoint-url')!.value = fullURL;
  click(dialog, '#endpoint-confirm');
  await settle();
  assert.equal(saved.endpoints[1].apiFormat, 'custom');
  assert.equal(saved.endpoints[1].baseUrl, fullURL);
  click(root, '[data-action="edit-endpoint"][data-id="legacy"]');
  dialog = document.querySelector('dialog')!;
  format = dialog.querySelector<HTMLSelectElement>('#endpoint-api-format')!;
  assert.equal(format.value, '');
  assert.equal(format.options.length, 6);
  click(dialog, '#endpoint-confirm');
  await settle();
  assert.equal(saved.endpoints[0].apiFormat, '', 'saving old endpoints must retain their URL behavior');
  assert.equal(saved.endpoints[0].baseUrl, 'https://relay.invalid/v1');
  click(root, '[data-router-tab="models"]');
  click(root, '#add-model');
  dialog = document.querySelector('dialog')!;
  const endpointSelect = dialog.querySelector<HTMLSelectElement>('#model-endpoint')!;
  endpointSelect.value = saved.endpoints[1].id;
  endpointSelect.dispatchEvent(new dom.window.Event('change'));
  assert.equal(dialog.querySelector<HTMLButtonElement>('#fetch-models')!.disabled, true);
  assert.equal(dialog.querySelector<HTMLButtonElement>('#model-confirm')!.disabled, false);
  dialog.querySelector<HTMLInputElement>('#model-upstream')!.value = 'synthetic-custom-model';
  click(dialog, '#model-confirm');
  await settle();
  assert.equal(dialog.isConnected, false, 'custom endpoints must allow manually entered models to save');
  assert.equal(saved.models[0].endpointId, saved.endpoints[1].id);
  assert.equal(saved.models[0].upstreamModel, 'synthetic-custom-model');
  console.log('PASS: endpoint formats, custom model saves, and legacy editing');
} finally {
  document.querySelector('dialog')?.remove();
  stop?.();
  for (const [key, descriptor] of previous) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor);
    else Reflect.deleteProperty(globalThis, key);
  }
  dom.window.close();
}
