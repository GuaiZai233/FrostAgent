import { Buffer } from 'node:buffer';
import { createRequire } from 'node:module';
import { dirname, resolve } from 'node:path';
const require = createRequire(import.meta.url);
const esbuildPath = require.resolve('esbuild', {
  paths: [dirname(require.resolve('vite'))],
});
const { build } = require(esbuildPath);
for (const testFile of [
  'instance-api.test.ts',
  'overview-enhancements.test.ts',
  'overview-page.test.ts',
  'stickers-page.test.ts',
  'frontend-editors.test.ts',
  'actionscat-disabled.test.ts',
  'actionscat-page.test.ts',
  'model-router-editor.test.ts',
  'prompt-merge.test.ts',
  'group-model-config.test.ts',
  'prompt-page.test.ts',
  'dialogue-page.test.ts',
  'skills.test.ts',
  'backend-settings-page.test.ts',
  'bot-user-id-list.test.ts',
  'control-auth.test.ts',
  'backup-download.test.ts',
  'storage-settings-page.test.ts',
  'authenticated-resources.test.ts',
]) {
  const result = await build({
    entryPoints: [resolve('tests', testFile)],
    bundle: true,
    platform: 'node',
    format: 'esm',
    write: false,
    loader: { '.css': 'empty' },
    alias: {
      '@frostagent/proto': resolve('../../libs/frostagent-proto/src/index.ts'),
    },
  });
  await import(
    'data:text/javascript;base64,' +
      Buffer.from(result.outputFiles[0].contents).toString('base64')
  );
}
await import('./layout.test.mjs');
