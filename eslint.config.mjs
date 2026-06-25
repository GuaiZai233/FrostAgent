import js from '@eslint/js';
import angular from 'angular-eslint';
import prettier from 'eslint-config-prettier';
import tseslint from 'typescript-eslint';

const tsFiles = ['**/*.ts'];
const htmlFiles = ['**/*.html'];
const scopeTo = (configs, files) =>
  configs.map((config) => ({
    ...config,
    files: config.files ?? files,
  }));

export default tseslint.config(
  {
    ignores: [
      '**/dist/**',
      '**/out-tsc/**',
      '.angular/**',
      '.nx/**',
      'bin/**',
      'gen/**',
      'internal/frontend/dist/**',
      'libs/frostagent-proto/src/generated/**',
      'node_modules/**',
    ],
  },
  js.configs.recommended,
  ...scopeTo(tseslint.configs.recommended, tsFiles),
  ...scopeTo(angular.configs.tsRecommended, tsFiles),
  {
    files: ['**/*.ts'],
    processor: angular.processInlineTemplates,
    rules: {
      '@angular-eslint/directive-selector': [
        'error',
        {
          type: 'attribute',
          prefix: 'app',
          style: 'camelCase',
        },
      ],
      '@angular-eslint/component-selector': [
        'error',
        {
          type: 'element',
          prefix: 'app',
          style: 'kebab-case',
        },
      ],
    },
  },
  ...scopeTo(angular.configs.templateRecommended, htmlFiles),
  {
    files: ['**/*.html'],
    rules: {},
  },
  prettier,
);
