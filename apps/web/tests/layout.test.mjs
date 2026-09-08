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

process.stdout.write(
  'PASS: sidebar controls remain inside the scaled viewport\n',
);
