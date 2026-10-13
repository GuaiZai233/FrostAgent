import { strict as assert } from 'node:assert';
import { readFile } from 'node:fs/promises';
import process from 'node:process';
import { URL } from 'node:url';

const main = await readFile(new URL('../src/main.ts', import.meta.url), 'utf8');
const base = await readFile(
  new URL('../src/styles/base.css', import.meta.url),
  'utf8',
);
const layout = await readFile(
  new URL('../src/styles/layout.css', import.meta.url),
  'utf8',
);
const overview = await readFile(
  new URL('../src/pages/overview.ts', import.meta.url),
  'utf8',
);
const chat = await readFile(
  new URL('../src/pages/chat.ts', import.meta.url),
  'utf8',
);
const promptPage = await readFile(
  new URL('../src/pages/prompt.ts', import.meta.url),
  'utf8',
);
const backendSettings = await readFile(
  new URL('../src/pages/backend-settings.ts', import.meta.url),
  'utf8',
);
const backendSettingsForm = await readFile(
  new URL('../src/pages/backend-settings-form.ts', import.meta.url),
  'utf8',
);
const settingFields = await readFile(
  new URL('../src/pages/backend-setting-fields.ts', import.meta.url),
  'utf8',
);
const settingsService = await readFile(
  new URL('../../../internal/service/settings/service.go', import.meta.url),
  'utf8',
);
const dialoguePage = await readFile(
  new URL('../src/pages/dialogue.ts', import.meta.url),
  'utf8',
);
assert.doesNotMatch(backendSettings, /edit-env-secret-cb/, 'setting secrecy is fixed by the server registry');
const components = await readFile(
  new URL('../src/styles/components.css', import.meta.url),
  'utf8',
);

const desktopFooter = main.match(
  /<div class="sidebar-footer">([\s\S]*?)<\/div>\s*<\/aside>/,
)?.[1];
assert.ok(desktopFooter, 'desktop sidebar footer is missing');
const instanceIndex = desktopFooter.indexOf('instance-manager-btn');
assert.ok(instanceIndex >= 0, 'desktop instance management control is missing');
assert.equal(
  main.includes('theme-toggle-desktop'),
  false,
  'desktop theme shortcut must not be rendered in the app shell',
);
assert.ok(
  main.includes('theme-toggle-mobile'),
  'mobile theme control was removed with the desktop shortcut',
);

assert.match(base, /--ui-zoom:\s*1\.1/);
assert.match(
  base,
  /html,\s*body\s*{[^}]*min-height:\s*calc\(100dvh \/ var\(--ui-zoom\)\);/,
);
assert.doesNotMatch(layout, /--ui-scale/);
assert.match(
  layout,
  /\.app-layout\s*{[^}]*min-height:\s*calc\(100dvh \/ var\(--ui-zoom\)\);/,
);
assert.match(
  layout,
  /\.desktop-sidebar\s*{[^}]*height:\s*calc\(100dvh \/ var\(--ui-zoom\)\);/,
);
assert.match(
  layout,
  /\.main-content\s*{[^}]*min-height:\s*calc\(100dvh \/ var\(--ui-zoom\)\);/,
);
assert.match(layout, /\.sidebar-nav\s*{[\s\S]*?min-height:\s*0;/);
assert.match(
  layout,
  /\.instance-selected-label\s*{[\s\S]*?text-overflow:\s*ellipsis;[\s\S]*?white-space:\s*nowrap;/,
);

assert.match(
  overview,
  /OneBot:\s*\$\{escapeHtml\(oneBotURL\)\}\s*<a\s[^>]*data-copy-url="\$\{escapeHtml\(oneBotURL\)\}"[^>]*>复制<\/a>/,
  'OneBot WebSocket URL is missing the copy link',
);
assert.match(
  overview,
  /AstrBot:\s*\$\{escapeHtml\(astrBotURL\)\}\s*<a\s[^>]*data-copy-url="\$\{escapeHtml\(astrBotURL\)\}"[^>]*>复制<\/a>/,
  'AstrBot WebSocket URL is missing the copy link',
);
assert.match(
  overview,
  /copyToClipboard\(target\)/,
  'overview copy action does not call copyToClipboard',
);

assert.match(
  chat,
  /<h1 class="page-title">直接对话<\/h1>[\s\S]*?<p class="text-xs text-muted mt-1">此模式通信断电全丢，无持久化<\/p>/,
  'direct chat must show the ephemeral-mode subtitle below its title',
);
assert.equal(
  chat.includes('断电全丢模式 (Mock Adapter)'),
  false,
  'direct chat must not render the redundant ephemeral-mode info card',
);

for (const removedSampleToken of [
  'SAMPLE_GROUP_PROMPT',
  'prompt-load-sample-btn',
  'prompt-empty-load-sample-btn',
  '示例 Prompt',
  'brand-icon',
]) {
  assert.equal(
    promptPage.includes(removedSampleToken),
    false,
    `prompt page must not retain sample UI: ${removedSampleToken}`,
  );
}
assert.match(
  promptPage,
  /<h3 class="text-sm font-semibold text-foreground mb-1">请选择群聊会话<\/h3>\s*<p class="text-xs max-w-sm mb-4">从左侧选择/,
  'prompt page empty state must retain its session-selection guidance',
);

const globalSettings = backendSettings.match(
  /const globalKeys = new Set\(\[([\s\S]*?)\]\);/,
)?.[1];
assert.ok(globalSettings?.includes("'SANDBOX_ENABLED'"));
assert.doesNotMatch(
  backendSettings,
  /controlPlaneRestartKeys|restartRequired/,
  'the SQL settings page must not show manual restart state',
);
assert.equal(
  globalSettings?.includes("'SYSTEM_PROMPT'"),
  false,
  'SYSTEM_PROMPT must be an instance-level setting, not in globalKeys',
);
assert.match(main, /path: '\/dialogue', label: '人格设定'/);
assert.match(dialoguePage, /<h1 class="page-title">人格设定<\/h1>/);
assert.match(dialoguePage, /id="system-prompt-edit-btn"[^>]*title="编辑系统提示词"/);
assert.match(dialoguePage, /id="system-prompt-display" class="[^"]*bg-muted[^"]*text-muted/);
assert.match(backendSettings, /id="settings-form"/);
assert.match(backendSettings, /额外 env 注入/);
assert.match(backendSettings, /id="sticker-activity-number"[^>]*type="number"|type="number" id="sticker-activity-number"/);
for (const id of ['group-mention-cb', 'group-at-cb', 'group-reply-cb']) {
  assert.match(backendSettings, new RegExp(`id="${id}" role="switch"`));
}
assert.match(backendSettingsForm, /class="toggle-switch"/);
assert.match(backendSettingsForm, /kind === 'number' \? 'number'/);
assert.doesNotMatch(backendSettings, /<table class="table env-table">|<p[^>]*>GROUP_REPLY_ON_MENTION<\/p>/);
assert.match(settingFields, /BOT_NAME: \{ label: '机器人名称'/);
const knownSettings = settingsService.match(/var knownEnvVars = map\[string\]envEntry\{([\s\S]*?)\n\}/)?.[1] ?? '';
const dedicatedSettings = settingFields.match(/export const dedicatedSettingKeys = new Set\(\[([\s\S]*?)\]\);/)?.[1] ?? '';
for (const [, key] of knownSettings.matchAll(/^\s*"([A-Z_]+)":/gm)) {
  if (['DIALOGUE_PATH', 'BRAIN_PATH', 'ONEBOT_WS_PATH', 'ASTRBOT_WS_PATH'].includes(key)) continue;
  assert.ok(
    new RegExp(`\\b${key}: \\{`).test(settingFields) || dedicatedSettings.includes(`'${key}'`),
    `missing Chinese setting field or dedicated control for ${key}`,
  );
}
for (const key of ['MAX_CONTEXT_CHARS', 'CODER_API_KEY']) {
  assert.equal(dedicatedSettings.includes(`'${key}'`), false, `${key} must appear in the general settings form`);
}

assert.match(
  backendSettings,
  /id="proactive-reply-cb"\s+role="switch"\s+aria-label="主动回复"/,
  'proactive reply control must use an accessible switch',
);
assert.match(
  backendSettings,
  /id="proactive-whitelist-cb"\s+role="switch"\s+aria-label="群聊白名单模式"/,
  'proactive whitelist control must use an accessible switch',
);
assert.match(
  components,
  /\.toggle-switch span\s*{[^}]*border-radius:\s*2rem;/,
  'toggle switch track must use a rounded pill shape',
);
for (const removedText of [
  '未被唤醒的入站群聊消息先 roll 概率；触发后由模型研判值得插嘴则回复，否则调用 stay_silent 静默',
  '精度 0.01 ~ 1.00，开启后最低 0.01，支持滑块或手动输入',
  '(ENABLE_PROACTIVE_REPLY_WHITELIST)',
  '已启用',
  '已停用',
]) {
  assert.equal(
    backendSettings.includes(removedText),
    false,
    `backend settings must not render removed text: ${removedText}`,
  );
}

process.stdout.write(
  'PASS: sidebar controls remain inside the scaled viewport\n',
);
