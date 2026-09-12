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
const backendSettings = await readFile(
  new URL('../src/pages/backend-settings.ts', import.meta.url),
  'utf8',
);
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

const globalSettings = backendSettings.match(
  /const globalKeys = new Set\(\[([\s\S]*?)\]\);/,
)?.[1];
const controlPlaneRestartSettings = backendSettings.match(
  /const controlPlaneRestartKeys = new Set\(\[([\s\S]*?)\]\);/,
)?.[1];
assert.ok(globalSettings?.includes("'SANDBOX_ENABLED'"));
assert.equal(
  controlPlaneRestartSettings?.includes("'SANDBOX_ENABLED'"),
  false,
  'SANDBOX_ENABLED must be shown as a hot Control Plane setting',
);
assert.equal(
  globalSettings?.includes("'SYSTEM_PROMPT'"),
  false,
  'SYSTEM_PROMPT must be an instance-level setting, not in globalKeys',
);

assert.match(
  backendSettings,
  /<table class="table env-table">/,
  'backend settings table must use env-table class for fixed wrapping layout',
);
assert.match(
  backendSettings,
  /whitespace-pre-wrap/,
  'backend settings display values must support whitespace-pre-wrap to wrap long values',
);
assert.match(
  components,
  /\.env-table\s*{[^}]*table-layout:\s*fixed;/,
  '.env-table must have table-layout: fixed to prevent wide columns from breaking layout',
);
assert.match(
  components,
  /\.env-table\s+td\s*{[^}]*overflow-wrap:\s*anywhere;/,
  '.env-table cells must have overflow-wrap: anywhere for responsive wrapping',
);

// Cascade regression check: ensure .table.env-table td resolves to vertical-align: top
function computeSpecificity(selector) {
  const ids = (selector.match(/#[a-zA-Z0-9_-]+/g) || []).length;
  const classes = (selector.match(/\.[a-zA-Z0-9_-]+/g) || []).length;
  const attrs = (selector.match(/\[[^\]]+\]/g) || []).length;
  const pseudos = (selector.match(/:[a-zA-Z0-9_-]+/g) || []).length;
  const stripped = selector.replace(
    /#[a-zA-Z0-9_-]+|\.[a-zA-Z0-9_-]+|\[[^\]]+\]|:[a-zA-Z0-9_-]+/g,
    '',
  );
  const elements = (stripped.match(/[a-zA-Z0-9_-]+/g) || []).length;
  return [ids, classes + attrs + pseudos, elements];
}

function matchesTarget(selector, target) {
  const parts = selector.trim().split(/\s+/);
  if (parts.length === 0) return false;
  const subject = parts[parts.length - 1];
  const subjectTag = subject.match(/^[a-zA-Z0-9_-]+/)?.[0];
  if (subjectTag && subjectTag !== target.tag) return false;
  const subjectClasses = (subject.match(/\.[a-zA-Z0-9_-]+/g) || []).map((c) =>
    c.slice(1),
  );
  if (subjectClasses.some((c) => !(target.classes || []).includes(c)))
    return false;

  if (parts.length > 1) {
    const ancestorPart = parts[0];
    const ancestorTag = ancestorPart.match(/^[a-zA-Z0-9_-]+/)?.[0];
    const ancestorClasses = (
      ancestorPart.match(/\.[a-zA-Z0-9_-]+/g) || []
    ).map((c) => c.slice(1));
    const match = (target.ancestors || []).some((anc) => {
      if (ancestorTag && anc.tag !== ancestorTag) return false;
      return ancestorClasses.every((c) => (anc.classes || []).includes(c));
    });
    if (!match) return false;
  }
  return true;
}

function resolveEffectiveProperty(cssText, targetElement, propertyName) {
  const cleanCss = cssText.replace(/\/\*[\s\S]*?\*\//g, '');
  const ruleRegex = /([^{}]+)\{([^{}]+)\}/g;
  let match;
  let ruleIndex = 0;
  const matchingDeclarations = [];

  while ((match = ruleRegex.exec(cleanCss)) !== null) {
    const rawSelectors = match[1];
    const body = match[2];
    ruleIndex++;

    const propRegex = new RegExp(
      `${propertyName}\\s*:\\s*([^;!]+)(?:!\\s*important)?\\s*;`,
      'i',
    );
    const propMatch = body.match(propRegex);
    if (!propMatch) continue;

    const val = propMatch[1].trim();
    const isImportant = /!\s*important/i.test(
      body.slice(propMatch.index, propMatch.index + propMatch[0].length),
    );

    const selectors = rawSelectors.split(',').map((s) => s.trim());
    for (const selector of selectors) {
      if (matchesTarget(selector, targetElement)) {
        const specificity = computeSpecificity(selector);
        matchingDeclarations.push({
          selector,
          val,
          isImportant,
          specificity,
          ruleIndex,
        });
      }
    }
  }

  matchingDeclarations.sort((a, b) => {
    if (a.isImportant !== b.isImportant) {
      return a.isImportant ? 1 : -1;
    }
    for (let i = 0; i < 3; i++) {
      if (a.specificity[i] !== b.specificity[i]) {
        return a.specificity[i] - b.specificity[i];
      }
    }
    return a.ruleIndex - b.ruleIndex;
  });

  return matchingDeclarations[matchingDeclarations.length - 1] || null;
}

const tableTdIndex = components.indexOf('.table td');
const envTableTdIndex = components.indexOf('.table.env-table td');
assert.ok(
  tableTdIndex >= 0,
  '.table td base rule must exist in components.css',
);
assert.ok(
  envTableTdIndex > tableTdIndex,
  '.table.env-table td rule must appear after .table td in stylesheet order to win cascade',
);

const effectiveTdVerticalAlign = resolveEffectiveProperty(
  components,
  {
    tag: 'td',
    classes: ['align-top'],
    ancestors: [{ tag: 'table', classes: ['table', 'env-table'] }],
  },
  'vertical-align',
);
assert.ok(
  effectiveTdVerticalAlign,
  'must find matching vertical-align rule for env table td',
);
assert.equal(
  effectiveTdVerticalAlign.val,
  'top',
  `effective cascade vertical-align on .table.env-table td must be "top", but got "${effectiveTdVerticalAlign.val}" from "${effectiveTdVerticalAlign.selector}"`,
);

process.stdout.write(
  'PASS: sidebar controls remain inside the scaled viewport\n',
);
