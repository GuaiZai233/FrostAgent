import assert from 'node:assert/strict';
import { mountDialoguePage } from '../src/pages/dialogue';
import { toast } from '../src/components/toast';
import type { createInstanceAPI } from '../src/api/client';

class MockElement {
  innerHTML = '';
  textContent = '';
  value = '';
  disabled = false;
  style: Record<string, string> = {};
  private listeners = new Map<string, () => void>();
  private children = new Map<string, MockElement>();

  addEventListener(event: string, listener: () => void) {
    this.listeners.set(event, listener);
  }
  click() {
    if (!this.disabled) this.listeners.get('click')?.();
  }
  input() { this.listeners.get('input')?.(); }
  focus() {}
  querySelector(selector: string): MockElement {
    if (!this.children.has(selector))
      this.children.set(selector, new MockElement());
    return this.children.get(selector)!;
  }
  querySelectorAll(): MockElement[] {
    return [];
  }
}

const errors: string[] = [];
const successes: string[] = [];
toast.error = (message) => {
  errors.push(message);
};
toast.success = (message) => {
  successes.push(message);
};

let storedPrompt = '原有人格\n第二行';
let saveSucceeds = true;
let deferSave = false;
let resolvePendingSave: (() => void) | undefined;
let envLoads = 0;
let dialogueLoads = 0;
let storedName = '示例 Bot';
let storedAliases = '示例,小狐狸';
const identityUpdates: Array<Array<{ key: string; value: string }>> = [];
const updates: Array<{ key: string; value: string; isSecret: boolean }> = [];
const api = {
  async listEnvVars() {
    envLoads++;
    return [
      { key: 'SYSTEM_PROMPT', value: storedPrompt, isSecret: false },
      { key: 'BOT_NAME', value: storedName, isSecret: false },
      { key: 'BOT_ALIASES', value: storedAliases, isSecret: false },
    ];
  },
  async listDialogues() {
    dialogueLoads++;
    return { dialogues: [], promptPreview: '', filePath: '' };
  },
  async updateEnvVar(update: {
    key: string;
    value: string;
    isSecret: boolean;
  }) {
    updates.push(update);
    if (deferSave) {
      return new Promise<{ success: boolean; error: string }>((resolve) => {
        resolvePendingSave = () => {
          storedPrompt = update.value;
          resolve({ success: true, error: '' });
        };
      });
    }
    if (!saveSucceeds) return { success: false, error: 'write failed' };
    storedPrompt = update.value;
    return { success: true, error: '' };
  },
  async updateEnvVars(changes: Array<{ key: string; value: string }>, signal: AbortSignal) {
    signal.throwIfAborted();
    identityUpdates.push(changes);
    if (!saveSucceeds) return { success: false, error: 'identity write failed' };
    for (const change of changes) {
      if (change.key === 'BOT_NAME') storedName = change.value;
      if (change.key === 'BOT_ALIASES') storedAliases = change.value;
    }
    return { success: true, error: '' };
  },
} as unknown as ReturnType<typeof createInstanceAPI>;

const settle = () => new Promise<void>((resolve) => setTimeout(resolve, 0));
const root = new MockElement();
const cleanup = mountDialoguePage(root as unknown as HTMLElement, api);
const display = root.querySelector('#system-prompt-display');
const editor = root.querySelector('#system-prompt-editor');
const input = root.querySelector('#system-prompt-input');
const edit = root.querySelector('#system-prompt-edit-btn');
const save = root.querySelector('#system-prompt-save-btn');
const cancel = root.querySelector('#system-prompt-cancel-btn');
const refresh = root.querySelector('#dialogue-refresh-btn');

assert.equal(
  edit.disabled,
  true,
  'editing is disabled until the current instance loads',
);
await settle();
assert.equal(display.textContent, storedPrompt);
assert.equal(display.style.display, 'block');
assert.equal(edit.disabled, false);
const name = root.querySelector('#persona-bot-name');
const aliases = root.querySelector('#persona-bot-aliases');
const identitySave = root.querySelector('#persona-identity-save');
assert.equal(name.value, storedName);
assert.equal(aliases.value, storedAliases);
name.value = '草稿名称';
name.input();
aliases.value = '草稿别名';
aliases.input();
assert.equal(identityUpdates.length, 0, 'identity typing must not save');
refresh.click();
await settle();
assert.equal(name.value, '草稿名称', 'refresh must preserve identity drafts');
identitySave.click();
await settle();
assert.deepEqual(identityUpdates[0].map(({ key, value }) => ({ key, value })), [
  { key: 'BOT_NAME', value: '草稿名称' }, { key: 'BOT_ALIASES', value: '草稿别名' },
]);
assert.equal(identitySave.disabled, true);
saveSucceeds = false;
aliases.value = '失败草稿';
aliases.input();
identitySave.click();
await settle();
assert.equal(aliases.value, '失败草稿');
assert.equal(identitySave.disabled, false);
assert.equal(storedAliases, '草稿别名');
aliases.value = storedAliases;
aliases.input();
saveSucceeds = true;
const loadsBeforePromptEdit = envLoads;
const dialogueLoadsBeforePromptEdit = dialogueLoads;

edit.click();
assert.equal(input.value, '原有人格\n第二行');
assert.equal(editor.style.display, 'flex');
input.value = '未保存草稿';
refresh.click();
await settle();
assert.equal(editor.style.display, 'flex', 'refresh keeps the prompt editor open');
assert.equal(input.value, '未保存草稿', 'refresh preserves the unsaved draft');
assert.equal(envLoads, loadsBeforePromptEdit, 'refresh does not reload the prompt during editing');
assert.equal(dialogueLoads, dialogueLoadsBeforePromptEdit + 1, 'refresh still reloads dialogue examples');

input.value = '新的人格\n第二行';
deferSave = true;
save.click();
assert.equal(input.disabled, true, 'typing is blocked during a pending save');
assert.equal(save.disabled, true);
assert.equal(cancel.disabled, true);
assert.equal(refresh.disabled, true);
resolvePendingSave?.();
await settle();
deferSave = false;
assert.deepEqual(updates[0], {
  key: 'SYSTEM_PROMPT',
  value: '新的人格\n第二行',
  isSecret: false,
});
assert.equal(display.textContent, '新的人格\n第二行');
assert.equal(editor.style.display, 'none');
assert.equal(input.disabled, false);
assert.equal(successes.at(-1), '系统提示词已保存并生效');

saveSucceeds = false;
edit.click();
input.value = '未保存的人格';
save.click();
await settle();
assert.equal(
  editor.style.display,
  'flex',
  'failed writes keep the draft editable',
);
assert.equal(input.value, '未保存的人格');
assert.match(errors.at(-1) || '', /write failed/);
cancel.click();
assert.equal(
  display.textContent,
  '新的人格\n第二行',
  'cancel restores the last saved value',
);

storedPrompt = '外部更新';
refresh.click();
await settle();
assert.equal(
  display.textContent,
  '外部更新',
  'refresh loads the latest instance value',
);
cleanup();

process.stdout.write(
  'PASS: persona system prompt display, save, failure, and refresh\n',
);
