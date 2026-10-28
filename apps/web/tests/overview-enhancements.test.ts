import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { renderOverviewTools } from '../src/pages/overview-tools';
import { renderInstanceLabel, renderInstanceStatus } from '../src/components/instance-label';
import { instanceState } from '../src/instance-state';

const tools = Array.from({ length: 12 }, (_, index) => ({ name: `tool_${index}`, description: `Description ${index}` }));
assert.match(renderOverviewTools([]), /暂无已注册工具/);
for (const count of [1, 10]) {
  const html = renderOverviewTools(tools.slice(0, count));
  assert.equal((html.match(/<article /g) || []).length, count);
  assert.doesNotMatch(html, /overview-all-tools/);
}
const preview = renderOverviewTools(tools);
assert.equal((preview.match(/<article /g) || []).length, 10);
assert.match(preview, /展示所有/);
assert.doesNotMatch(preview, /tool_10/);
assert.ok(preview.lastIndexOf('展示所有') > preview.lastIndexOf('</article>'));
const all = renderOverviewTools(tools, false);
assert.equal((all.match(/<article /g) || []).length, 12);
assert.match(all, /tool_11/);
assert.doesNotMatch(all, /id="overview-all-tools"/);
const { JSDOM } = createRequire(resolve('package.json'))('jsdom');
const maliciousTools = [
  { name: '<script>alert(1)</script>', description: '<img src=x onerror=alert(1)>' },
  { name: '<SCRIPT>alert(1)</SCRIPT>', description: '<IMG SRC=x ONERROR=alert(1)>' },
  { name: '<ScRiPt src="https://example.invalid/script"></ScRiPt>', description: '<ImG\nSrC=x\nOnErRoR=alert(1)>' },
];
const parsedTools = new JSDOM(renderOverviewTools(maliciousTools));
try {
  const rendered = parsedTools.window.document;
  assert.equal(rendered.querySelector('script, img'), null, 'tool content must remain text, not executable HTML');
  const cards = rendered.querySelectorAll('article');
  for (const [index, tool] of maliciousTools.entries()) {
    assert.equal(cards[index].querySelector('h3')?.textContent, tool.name);
    assert.equal(cards[index].querySelector('p')?.textContent, tool.description);
  }
} finally {
  parsedTools.window.close();
}

assert.match(renderInstanceStatus(true), /instance-dot on/);
assert.match(renderInstanceStatus(false), /instance-dot off/);
assert.match(renderInstanceLabel(null), /未选择/);
const instance = { id: 'fixture-a', name: '<Example>', enabled: false, created_at: '' };
assert.match(renderInstanceLabel(instance), /instance-dot off/);
assert.match(renderInstanceLabel(instance), /&lt;Example&gt;/);

const events = new EventTarget();
Object.assign(globalThis, { window: Object.assign(events, { location: { origin: 'https://panel.example' } }), document: { querySelectorAll: () => [] } });
let updated = 0;
let selected = 0;
events.addEventListener('instance-updated', () => updated++);
events.addEventListener('instance-selected', () => selected++);
instanceState.select(instance);
globalThis.fetch = async () => Response.json({ instances: [{ ...instance, enabled: true }], next_name: 'Example' });
await instanceState.refresh();
assert.equal(updated, 1, 'enable changes update the shell status');
assert.equal(selected, 1, 'status refresh must not trigger route remounts');
assert.equal(instanceState.selected?.enabled, true);
await instanceState.refresh();
assert.equal(updated, 1, 'unchanged status should not repeatedly notify');

const main = readFileSync('src/main.ts', 'utf8');
const overview = readFileSync('src/pages/overview.ts', 'utf8');
const settings = readFileSync('src/pages/settings.ts', 'utf8');
const styles = readFileSync('src/styles/overview.css', 'utf8');
const dockerfile = readFileSync('../../Dockerfile', 'utf8');
const dockerignore = readFileSync('../../.dockerignore', 'utf8');
assert.match(main, /import logoURL from '\.\.\/\.\.\/\.\.\/assets\/logo.png'/);
const webStage = dockerfile.split(' AS web')[1]?.split(/\nFROM /)[0] ?? '';
const logoCopy = 'COPY assets/logo.png ./assets/logo.png';
assert.ok(webStage.includes(logoCopy), 'Docker web stage must copy the imported logo');
assert.ok(webStage.indexOf(logoCopy) < webStage.indexOf('pnpm --filter @frostagent/web run build'));
assert.match(dockerignore, /^\*\s*$/m, 'Docker context must retain its default-deny rule');
assert.match(dockerignore, /^!assets\/\r?\nassets\/\*\*\r?\n!assets\/logo\.png\s*$/m,
  'Docker context must reopen assets, exclude all descendants, then allow only the logo');
assert.ok(!dockerignore.split(/\r?\n/).includes('!assets/**'), 'only the logo should be added to the Docker context');
assert.equal((main.match(/<img src="\$\{logoURL\}"/g) || []).length, 3);
assert.ok(main.indexOf("path: '/security'") < main.indexOf("path: '/settings'"));
assert.doesNotMatch(overview, /openQuickConfig|id="quick-config"/);
assert.ok(settings.indexOf('id="settings-quick-config"') < settings.indexOf('href="#/settings/backup"'));
assert.match(settings, /void openQuickConfig\(\)/);
for (const url of ['https://github.com/GuaiZai233/FrostAgent', 'https://frostagent.guaitech.org/docs']) {
  assert.ok(overview.includes(`href="${url}" target="_blank" rel="noopener noreferrer"`));
}
assert.match(styles, /\.overview-tools-strip\s*\{[^}]*overflow-x: auto/);
assert.match(overview, /scrollLeft = toolsScroll/);
assert.match(overview, /closeToolsDialog\?\.\(\)/);
console.log('PASS: overview tools, resources, navigation, quick configuration and live instance status');
