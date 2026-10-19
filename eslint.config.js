import js from '@eslint/js';
import stylistic from '@stylistic/eslint-plugin';
import { defineConfig, globalIgnores } from 'eslint/config';
import prettier from 'eslint-config-prettier/flat';
import reactHooks from 'eslint-plugin-react-hooks';
import globals from 'globals';
import ts from 'typescript-eslint';

// Separate declarations while keeping imports, re-exports, and small constants grouped.
const declarations = [
  {
    selector: [
      'FunctionDeclaration',
      'ClassDeclaration',
      'TSInterfaceDeclaration',
      'TSEnumDeclaration',
      'ExportNamedDeclaration[declaration.type=/^(FunctionDeclaration|ClassDeclaration|TSInterfaceDeclaration|TSEnumDeclaration)$/]',
      'ExportDefaultDeclaration[declaration.type=/^(FunctionDeclaration|ClassDeclaration|ArrowFunctionExpression)$/]',
      'ExportNamedDeclaration:has(ExportNamedDeclaration > VariableDeclaration > VariableDeclarator > :matches(ArrowFunctionExpression, FunctionExpression))',
    ].join(', '),
  },
  {
    selector:
      ':matches(TSTypeAliasDeclaration, ExportNamedDeclaration[declaration.type="TSTypeAliasDeclaration"])',
    lineMode: 'multiline',
  },
];

export default defineConfig(
  globalIgnores([
    '**/dist/**',
    '**/node_modules/**',
    '**/coverage/**',
    '**/test-results/**',
    '**/playwright-report/**',
    '**/data/**',
    '**/.data/**',
    '**/.cache/**',
    '**/.e2e/**',
  ]),
  {
    files: ['**/*.{js,mjs,cjs,ts,tsx}'],
    extends: [js.configs.recommended],
    plugins: { '@stylistic': stylistic },
    rules: {
      '@stylistic/padding-line-between-statements': [
        'error',
        { blankLine: 'always', prev: 'import', next: '*' },
        { blankLine: 'any', prev: 'import', next: 'import' },
        { blankLine: 'always', prev: '*', next: declarations },
        { blankLine: 'always', prev: declarations, next: '*' },
      ],
      eqeqeq: ['error', 'always', { null: 'ignore' }],
      'no-var': 'error',
      'object-shorthand': 'error',
      'prefer-const': ['error', { destructuring: 'all' }],
    },
  },
  {
    files: ['**/*.{ts,tsx}'],
    extends: [ts.configs.recommended],
    languageOptions: {
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
    rules: {
      '@typescript-eslint/consistent-type-imports': 'error',
      '@typescript-eslint/no-explicit-any': 'error',
      '@typescript-eslint/no-floating-promises': 'error',
      '@typescript-eslint/no-misused-promises': 'error',
      '@typescript-eslint/return-await': ['error', 'in-try-catch'],
      '@typescript-eslint/no-unused-vars': [
        'error',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_',
          caughtErrorsIgnorePattern: '^_',
        },
      ],
    },
  },
  {
    files: [
      'apps/dashboard/src/**/*.{ts,tsx}',
      'packages/nextstep/src/**/*.{ts,tsx}',
    ],
    ignores: ['**/*.test.{ts,tsx}'],
    languageOptions: { globals: globals.browser },
    plugins: { 'react-hooks': reactHooks },
    rules: {
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'error',
    },
  },
  {
    files: [
      '**/*.config.{js,mjs,ts}',
      'scripts/**/*.{js,mjs}',
      '**/*.test.{ts,tsx}',
      'tests/**/*.{ts,tsx}',
    ],
    languageOptions: { globals: globals.node },
  },
  prettier,
);
