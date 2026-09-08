import { Buffer } from 'node:buffer';
import { createRequire } from 'node:module';
import { dirname, resolve } from 'node:path';
const require = createRequire(import.meta.url);
const esbuildPath = require.resolve('esbuild', {
  paths: [dirname(require.resolve('vite'))],
});
const { build } = require(esbuildPath);
const result = await build({
  entryPoints: [resolve('tests/instance-api.test.ts')],
  bundle: true,
  platform: 'node',
  format: 'esm',
  write: false,
  alias: {
    '@frostagent/proto': resolve('../../libs/frostagent-proto/src/index.ts'),
  },
});
await import(
  'data:text/javascript;base64,' +
    Buffer.from(result.outputFiles[0].contents).toString('base64')
);
await import('./layout.test.mjs');
