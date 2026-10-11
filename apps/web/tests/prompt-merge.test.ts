import assert from 'node:assert/strict';
import { renderPromptInspector, type ParsedPrompt } from '../src/components/prompt-inspector';

const listeners = new Map<string, () => void>();
const container = {
  innerHTML: '',
  querySelector(selector: string) {
    return {
      addEventListener(_event: string, listener: () => void) {
        listeners.set(selector, listener);
      },
    };
  },
} as unknown as HTMLElement;

const prompt: ParsedPrompt = {
  raw: '{}',
  model: 'model-<name>',
  recentMessages: [{ role: 'assistant', sender: 'Bot', content: '回复', rawText: '回复' }],
  summaryGroups: [{
    summary: '摘要',
    parsedMessages: [{ role: 'assistant', sender: 'Bot', content: '之前的回复', rawText: '之前的回复' }],
  }],
  images: [],
  hasGroupMessages: true,
};

let summaryOpened = false;
renderPromptInspector(container, prompt, {
  onViewSummary: () => { summaryOpened = true; },
  modelConfigHref: '#/model-router?group_id=123',
});

assert.match(container.innerHTML, /id="pi-view-summary-btn"/);
assert.match(container.innerHTML, /群聊总结/);
assert.match(container.innerHTML, /href="#\/model-router\?group_id=123"/);
assert.match(container.innerHTML, /模型配置/);
assert.equal((container.innerHTML.match(/Bot \(model-&lt;name&gt;\)/g) || []).length, 2);
assert.doesNotMatch(container.innerHTML, /Assistant \/ Bot|badge-purple text-xs font-mono/);
listeners.get('#pi-view-summary-btn')?.();
assert.equal(summaryOpened, true);

renderPromptInspector(container, prompt);
assert.match(container.innerHTML, /Prompt Inspector/);
assert.doesNotMatch(container.innerHTML, /id="pi-view-summary-btn"|模型配置/);

renderPromptInspector(container, prompt, { showRawToggle: true, title: '<img src=x>' });
listeners.get('#pi-toggle-view-btn')?.();
assert.match(container.innerHTML, /&lt;img src=x&gt;/);
assert.doesNotMatch(container.innerHTML, /<img src=x>/);

process.stdout.write('PASS: merged session actions and Bot model label\n');
