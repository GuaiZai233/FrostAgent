import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { fileURLToPath, URL } from 'node:url';
import { build, createServer as createViteServer } from 'vite';

// Exercise the real production graph without replacing the embedded frontend.
const root = fileURLToPath(new URL('..', import.meta.url));
const result = await build({
  root,
  logLevel: 'silent',
  build: { write: false },
});
const { output } = result;
const chunks = output.filter((item) => item.type === 'chunk');
const entry = chunks.find((chunk) => chunk.isEntry);
assert.ok(entry, 'the dashboard must emit an entry chunk');
for (const chunk of chunks) {
  assert.ok(
    Buffer.byteLength(chunk.code) <= 500_000,
    `${chunk.fileName} exceeds Vite's default 500 kB warning threshold`,
  );
}

const eagerModules = new Set();
const visited = new Set();
function visitStaticImports(chunk) {
  if (visited.has(chunk.fileName)) return;
  visited.add(chunk.fileName);
  for (const id of Object.keys(chunk.modules)) {
    eagerModules.add(id.replaceAll('\\', '/'));
  }
  for (const name of chunk.imports) {
    const dependency = chunks.find((item) => item.fileName === name);
    if (dependency) visitStaticImports(dependency);
  }
}
visitStaticImports(entry);
for (const page of ['backend-settings', 'model-router']) {
  const suffix = `/src/pages/${page}.ts`;
  assert.ok(
    ![...eagerModules].some((id) => id.endsWith(suffix)),
    `${page} must not be loaded with the app shell`,
  );
  assert.ok(
    chunks.some(
      (chunk) =>
        chunk.isDynamicEntry &&
        chunk.facadeModuleId?.replaceAll('\\', '/').endsWith(suffix),
    ),
    `${page} must remain reachable as a lazy page`,
  );
}

const html = output.find((item) => item.fileName === 'index.html');
assert.equal(html?.type, 'asset');
const faviconURL = String(html.source).match(
  /<link rel="icon" type="image\/x-icon" href="([^"]+)"/,
)?.[1];
assert.equal(
  faviconURL,
  '/favicon.ico?v=fox-scarf',
  'the favicon must bypass the old Nx cache and work in dev and production',
);

// Generated from assets/logo.png, with frames for common browser tab sizes.
const ico = await readFile(new URL('../public/favicon.ico', import.meta.url));
assert.equal(ico.readUInt16LE(0), 0);
assert.equal(ico.readUInt16LE(2), 1);
assert.equal(ico.readUInt16LE(4), 3);
const sizes = [];
for (let index = 0; index < 3; index++) {
  const offset = 6 + index * 16;
  sizes.push(ico[offset]);
  assert.equal(ico[offset], ico[offset + 1], 'ICO frames must be square');
  const length = ico.readUInt32LE(offset + 8);
  const start = ico.readUInt32LE(offset + 12);
  assert.ok(start >= 54 && length > 0 && start + length <= ico.length);
}
assert.deepEqual(sizes.sort((a, b) => a - b), [16, 32, 48]);

// An out-of-root HTML image reference can build correctly but serve HTML in dev.
const dev = await createViteServer({
  root,
  logLevel: 'silent',
  server: { middlewareMode: true, watch: null, preTransformRequests: false },
  optimizeDeps: { noDiscovery: true, include: [] },
});
const server = createServer(dev.middlewares);
try {
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const base = `http://127.0.0.1:${server.address().port}`;
  const response = await globalThis.fetch(`${base}/`, {
    signal: globalThis.AbortSignal.timeout(5000),
  });
  assert.equal(response.status, 200);
  const devHTML = await response.text();
  assert.ok(devHTML.includes(`href="${faviconURL}"`));
  const iconResponse = await globalThis.fetch(new URL(faviconURL, base), {
    signal: globalThis.AbortSignal.timeout(5000),
  });
  assert.equal(iconResponse.status, 200);
  assert.match(iconResponse.headers.get('content-type') ?? '', /^image\//);
  assert.deepEqual(Buffer.from(await iconResponse.arrayBuffer()), ico);
} finally {
  await new Promise((resolve, reject) => {
    server.close((error) => error ? reject(error) : resolve());
    server.closeAllConnections();
  });
  await dev.close();
}
