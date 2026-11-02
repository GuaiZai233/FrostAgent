import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { create } from '@bufbuild/protobuf';
import { ModelRouterConfigurationSchema } from '@frostagent/proto';
import { ModelRouterEditorError } from '../src/api/model-router-editor';
import { toast } from '../src/components/toast';
import { mountDialoguePage } from '../src/pages/dialogue';
import { mountMCPPage } from '../src/pages/mcp';
import { mountModelRouterPage } from '../src/pages/model-router';
import { renderMarkdown } from '../src/utils/markdown';

const { JSDOM } = createRequire(resolve('package.json'))('jsdom');
const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'https://console.invalid/' });
const previousGlobals = new Map(['window', 'document'].map((key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
Object.defineProperty(globalThis, 'window', { configurable: true, value: dom.window });
Object.defineProperty(globalThis, 'document', { configurable: true, value: dom.window.document });
dom.window.HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
dom.window.HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
const settle = () => delay(0);
const root = (): HTMLElement => {
  const element = document.createElement('div');
  document.body.appendChild(element);
  return element;
};
const click = (container: ParentNode, selector: string) => {
  const button = container.querySelector<HTMLButtonElement>(selector);
  assert.ok(button, `missing button: ${selector}`);
  button.click();
};
const messages = { error: [] as string[], info: [] as string[], success: [] as string[] };
for (const level of ['error', 'info', 'success'] as const) {
  toast[level] = (message: string) => { messages[level].push(message); };
}

try {
  const preview = root();
  preview.innerHTML = renderMarkdown([
    '# Persona', '', '**Bold** and *emphasis* with `inline code`.', '',
    '- First', '- Second', '', '> Quote', '',
    '| Name | Value |', '| --- | --- |', '| tone | friendly |', '',
    '```html', '<script>alert(1)</script>', '```', '',
    '[Docs](https://example.invalid/docs)',
  ].join('\n'));
  assert.equal(preview.querySelector('h1')?.textContent, 'Persona');
  assert.equal(preview.querySelector('strong')?.textContent, 'Bold');
  assert.equal(preview.querySelectorAll('li').length, 2);
  assert.equal(preview.querySelector('blockquote')?.textContent?.trim(), 'Quote');
  assert.equal(preview.querySelectorAll('td').length, 2);
  assert.equal(preview.querySelector('pre code')?.textContent?.trim(), '<script>alert(1)</script>');
  assert.equal(preview.querySelector('a')?.getAttribute('href'), 'https://example.invalid/docs');
  assert.equal(preview.querySelector('script'), null);

  const attacks = [
    '<img src="https://tracker.invalid/pixel" onerror="alert(1)">',
    '<script>alert(1)</script><iframe src="https://tracker.invalid/frame"></iframe>',
    '[run](javascript:alert%281%29)',
    '[run](jav&#x61;script:alert%281%29)',
    '[run](data:text/html;base64,PHNjcmlwdD4=)',
    '![hidden](https://tracker.invalid/pixel)',
    '<svg><a xlink:href="javascript:alert(1)">run</a></svg>',
    '<style>body{background:url(https://tracker.invalid/style)}</style>',
    '<math><mtext><table><mglyph><style><!--</style><img title="--><img src=1 onerror=alert(1)>">',
  ];
  for (const attack of attacks) {
    preview.innerHTML = renderMarkdown(attack);
    assert.equal(preview.querySelector('script,img,iframe,svg,math,style,object,embed,link'), null, attack);
    for (const element of preview.querySelectorAll('*')) {
      for (const attribute of element.attributes) {
        assert.doesNotMatch(attribute.name, /^on|^src$|^style$/i, attack);
      }
    }
    for (const anchor of preview.querySelectorAll('a[href]')) {
      assert.doesNotMatch(anchor.getAttribute('href') ?? '', /^(?:javascript|data|vbscript):/i, attack);
    }
  }
  preview.remove();

  let savedPrompt = '# Saved persona\n\n- Be helpful';
  const promptRoot = root();
  const stopPrompt = mountDialoguePage(promptRoot, {
    async listEnvVars() { return [{ key: 'SYSTEM_PROMPT', value: savedPrompt }]; },
    async listDialogues() { return { dialogues: [], promptPreview: '', filePath: '' }; },
    async updateEnvVar({ value }: { value: string }) { savedPrompt = value; return { success: true }; },
  } as unknown as NonNullable<Parameters<typeof mountDialoguePage>[1]>);
  await settle();
  const display = promptRoot.querySelector<HTMLElement>('#system-prompt-display')!;
  const input = promptRoot.querySelector<HTMLTextAreaElement>('#system-prompt-input')!;
  assert.equal(display.querySelector('h1')?.textContent, 'Saved persona');
  assert.ok(display.classList.contains('system-prompt-viewport'));
  assert.equal(display.tabIndex, 0, 'scrollable preview must be keyboard accessible');
  assert.equal(input.rows, 15);
  const css = readFileSync(resolve('src/styles/prompt-preview.css'), 'utf8');
  assert.match(css, /height: calc\(22\.5em \+ 1\.5rem\)/);
  assert.match(css, /overflow: auto/);
  click(promptRoot, '#system-prompt-edit-btn');
  assert.equal(input.value, savedPrompt, 'editing must retain the original Markdown');
  const updatedPrompt = '# New persona\n\n**Bold**\n\n<img src=x onerror=alert(1)>';
  input.value = updatedPrompt;
  click(promptRoot, '#system-prompt-save-btn');
  await settle();
  assert.equal(savedPrompt, updatedPrompt, 'rendering must not mutate the persisted source');
  assert.equal(display.querySelector('h1')?.textContent, 'New persona');
  assert.equal(display.querySelector('strong')?.textContent, 'Bold');
  assert.equal(display.querySelector('img'), null);
  assert.match(display.textContent ?? '', /<img src=x onerror=alert\(1\)>/);
  stopPrompt();
  promptRoot.remove();

  const description = Array.from({ length: 100 }, (_, index) => `Description line ${index} <img src=x onerror=alert(1)>`).join('\n');
  const mcpRoot = root();
  const stopMCP = mountMCPPage(mcpRoot, {
    async listMCPServers() {
      return { servers: [{
        id: 'sample-server', name: 'Sample MCP', transportType: 'stdio',
        command: 'sample', args: [], enabled: true, status: 'connected',
        toolsCount: 1, enabledToolsCount: 1, tools: [{
          name: 'sample_tool', fullName: 'sample-server.sample_tool', enabled: true,
          description, parametersJson: '{"type":"object","properties":{"query":{"type":"string"}}}',
        }],
      }] };
    },
  } as unknown as NonNullable<Parameters<typeof mountMCPPage>[1]>);
  await settle();
  click(mcpRoot, '[data-action="toggle-expand"]');
  assert.equal(mcpRoot.querySelectorAll('thead th').length, 3);
  assert.doesNotMatch(mcpRoot.textContent ?? '', /Description line/);
  assert.equal(mcpRoot.querySelector('[data-action="view-schema"]'), null);
  assert.equal(mcpRoot.querySelector('[data-action="view-details"]')?.textContent?.trim(), '详情');
  click(mcpRoot, '[data-action="view-details"]');
  const details = document.querySelector('dialog')!;
  assert.match(details.querySelector('.dialog-title')?.textContent ?? '', /工具详情: sample_tool/);
  const detailsDescription = details.querySelector<HTMLElement>('.mcp-tool-description')!;
  assert.equal(detailsDescription.textContent, description, 'details must retain the complete long description');
  assert.equal(detailsDescription.style.overflow, 'auto');
  assert.equal(detailsDescription.style.maxHeight, '15rem');
  assert.equal(detailsDescription.tabIndex, 0);
  assert.equal(detailsDescription.querySelector('img'), null);
  assert.deepEqual(JSON.parse(details.querySelector('pre code')!.textContent!), {
    type: 'object', properties: { query: { type: 'string' } },
  });
  click(details, '.dialog-close-btn');
  stopMCP();
  mcpRoot.remove();

  const active = create(ModelRouterConfigurationSchema, {
    version: 1,
    endpoints: [{ id: 'provider', displayName: 'Published provider' }],
  });
  const draft = structuredClone(active);
  draft.endpoints[0].displayName = 'Unpublished provider';
  let discardCalls = 0;
  let failDiscard = false;
  const routerRoot = root();
  const stopRouter = mountModelRouterPage(routerRoot, {
    hasInstance: false,
    async getModelRouterState() {
      return { active, draft, hasDraftSecretChanges: true, loadError: '' };
    },
    async discardChanges(currentDraft: typeof draft) {
      discardCalls++;
      assert.equal(currentDraft.endpoints[0].displayName, failDiscard ? 'Unpublished provider' : 'Published provider');
      if (failDiscard) throw new ModelRouterEditorError('共享供应商改动已放弃；route unavailable', active, active, false);
      return active;
    },
  } as unknown as NonNullable<Parameters<typeof mountModelRouterPage>[1]>);
  await settle();
  assert.equal(routerRoot.querySelector('#router-reset-shared'), null);
  assert.equal(routerRoot.querySelector('#router-discard')?.textContent, '放弃改动');
  click(routerRoot, '#router-discard');
  let confirmation = document.querySelector('dialog')!;
  assert.match(confirmation.textContent ?? '', /所有页面.*共享供应商、模型与密钥.*当前实例.*路由/);
  click(confirmation, '#confirm-cancel-btn');
  await settle();
  assert.equal(discardCalls, 0, 'cancellation must not alter any draft');
  failDiscard = true;
  const infoCount = messages.info.length;
  click(routerRoot, '#router-discard');
  click(document.querySelector('dialog')!, '#confirm-ok-btn');
  await settle();
  assert.equal(discardCalls, 1);
  assert.equal(messages.info.length, infoCount, 'partial failure must not show a success message');
  assert.match(messages.error.at(-1) ?? '', /放弃改动失败.*route unavailable/);
  assert.match(routerRoot.textContent ?? '', /Published provider/, 'partial snapshot must be reflected');
  failDiscard = false;
  click(routerRoot, '#router-discard');
  click(document.querySelector('dialog')!, '#confirm-ok-btn');
  await settle();
  assert.equal(discardCalls, 2);
  assert.equal(messages.info.at(-1), '未发布的改动已放弃');
  click(routerRoot, '#router-discard');
  confirmation = document.querySelector('dialog')!;
  stopRouter();
  await settle();
  assert.equal(confirmation.isConnected, false, 'unmount must dismiss destructive confirmation');
  assert.equal(discardCalls, 2);
  routerRoot.remove();

  console.log('PASS: safe Markdown prompt preview, bounded MCP details, and unified discard UI');
} finally {
  for (const [key, descriptor] of previousGlobals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor);
    else Reflect.deleteProperty(globalThis, key);
  }
  dom.window.close();
}
