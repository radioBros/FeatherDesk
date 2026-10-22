import js from '@eslint/js'
import tseslint from 'typescript-eslint'
import vue from 'eslint-plugin-vue'
import vueParser from 'vue-eslint-parser'
import globals from 'globals'
import local from './eslint-rules/index.js'

export default [
  { ignores: ['dist/**', 'node_modules/**', 'eslint-rules/fixtures/**', 'src/api/schema.d.ts', 'playwright-report/**', 'test-results/**', 'coverage/**'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...vue.configs['flat/recommended'],
  {
    files: ['**/*.{ts,vue,js,mjs}'],
    languageOptions: {
      parser: vueParser,
      parserOptions: { parser: tseslint.parser, ecmaVersion: 'latest', sourceType: 'module', extraFileExtensions: ['.vue'] },
      globals: { ...globals.browser, ...globals.node },
    },
    plugins: { local },
    rules: {
      'local/rules-from-validations': 'error',
      'local/form-submit-validates': 'error',
      'vue/multi-word-component-names': 'off',
      'vue/require-default-prop': 'off',
      'vue/max-attributes-per-line': 'off',
      'vue/singleline-html-element-content-newline': 'off',
      'vue/html-self-closing': 'off',
      'vue/html-indent': 'off',
      'vue/html-closing-bracket-newline': 'off',
      'vue/multiline-html-element-content-newline': 'off',
      'vue/attributes-order': 'off',
      'vue/first-attribute-linebreak': 'off',
      '@typescript-eslint/no-explicit-any': 'off',
      '@typescript-eslint/no-empty-object-type': 'off',
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
    },
  },
  {
    // FE-007: permissions are never re-derived in components — use session getters
    // or the ticket's `allowed_actions`. Only the session store and route guards
    // may compare role / tier / kind.
    files: ['src/**/*.{ts,vue}'],
    ignores: ['src/stores/session.ts', 'src/router/guards.ts', 'src/**/*.spec.ts', 'src/test/**'],
    rules: {
      'no-restricted-syntax': [
        'error',
        {
          selector: "BinaryExpression > MemberExpression[property.name=/^(role|tier|kind)$/]:matches([object.name=/^(me|user|principal)$/], [object.property.name=/^(me|user|principal)$/], [object.expression.property.name=/^(me|user|principal)$/])",
          message: 'Do not compare the principal role/tier/kind in components; use the session store or allowed_actions (FE-007).',
        },
        {
          selector: "BinaryExpression > ChainExpression > MemberExpression[property.name=/^(role|tier|kind)$/]:matches([object.name=/^(me|user|principal)$/], [object.property.name=/^(me|user|principal)$/], [object.expression.property.name=/^(me|user|principal)$/])",
          message: 'Do not compare the principal role/tier/kind in components; use the session store or allowed_actions (FE-007).',
        },
        {
          selector: "BinaryExpression > TSNonNullExpression > MemberExpression[property.name=/^(role|tier|kind)$/]:matches([object.name=/^(me|user|principal)$/], [object.property.name=/^(me|user|principal)$/], [object.expression.property.name=/^(me|user|principal)$/])",
          message: 'Do not compare the principal role/tier/kind in components; use the session store or allowed_actions (FE-007).',
        },
      ],
    },
  },
  {
    files: ['eslint-rules/**/*.js', 'scripts/**/*.mjs'],
    rules: { '@typescript-eslint/no-require-imports': 'off' },
  },
]
