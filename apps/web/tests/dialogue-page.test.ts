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
const updates: Array<{ key: string; value: string; isSecret: boolean }> = [];
const api = {
  async listEnvVars() {
    return [{ key: 'SYSTEM_PROMPT', value: storedPrompt, isSecret: false }];
  },
  async listDialogues() {
    return { dialogues: [], promptPreview: '', filePath: '' };
  },
  async updateEnvVar(update: {
    key: string;
    value: string;
    isSecret: boolean;
  }) {
    updates.push(update);
    if (!saveSucceeds) return { success: false, error: 'write failed' };
    storedPrompt = update.value;
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

assert.equal(
  edit.disabled,
  true,
  'editing is disabled until the current instance loads',
);
await settle();
assert.equal(display.textContent, storedPrompt);
assert.equal(display.style.display, 'block');
assert.equal(edit.disabled, false);

edit.click();
assert.equal(input.value, '原有人格\n第二行');
assert.equal(editor.style.display, 'flex');
input.value = '新的人格\n第二行';
save.click();
await settle();
assert.deepEqual(updates[0], {
  key: 'SYSTEM_PROMPT',
  value: '新的人格\n第二行',
  isSecret: false,
});
assert.equal(display.textContent, '新的人格\n第二行');
assert.equal(editor.style.display, 'none');
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
root.querySelector('#dialogue-refresh-btn').click();
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
