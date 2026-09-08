import { strict as assert } from 'node:assert';
import { readFile } from 'node:fs/promises';

const main = await readFile(new URL('../src/main.ts', import.meta.url), 'utf8');
const base = await readFile(
  new URL('../src/styles/base.css', import.meta.url),
  'utf8',
);
const layout = await readFile(
  new URL('../src/styles/layout.css', import.meta.url),
  'utf8',
);

const desktopFooter = main.match(
  /<div class="sidebar-footer">([\s\S]*?)<\/div>\s*<\/aside>/,
)?.[1];
assert.ok(desktopFooter, 'desktop sidebar footer is missing');
const themeIndex = desktopFooter.indexOf('theme-toggle-desktop');
const instanceIndex = desktopFooter.indexOf('instance-manager-btn');
assert.ok(
  themeIndex >= 0,
  'desktop theme control was removed from its original footer',
);
assert.ok(instanceIndex >= 0, 'desktop instance management control is missing');
assert.ok(
  themeIndex < instanceIndex,
  'instance management displaced the existing theme control',
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

console.log('PASS: sidebar controls remain inside the scaled viewport');
