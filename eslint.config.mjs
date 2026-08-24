import js from '@eslint/js';
import prettier from 'eslint-config-prettier';
import tseslint from 'typescript-eslint';

const tsFiles = ['**/*.ts'];
const scopeTo = (configs, files) =>
  configs.map((config) => ({
    ...config,
    files: config.files ?? files,
  }));

export default tseslint.config(
  {
    ignores: [
      '.claude/**',
      '**/dist/**',
      '**/out-tsc/**',
      'bin/**',
      'gen/**',
      'internal/frontend/dist/**',
      'libs/frostagent-proto/src/generated/**',
      'node_modules/**',
    ],
  },
  js.configs.recommended,
  ...scopeTo(tseslint.configs.recommended, tsFiles),
  prettier,
);
