import js from '@eslint/js'
import vueI18n from '@intlify/eslint-plugin-vue-i18n'
import { defineConfigWithVueTs, vueTsConfigs } from '@vue/eslint-config-typescript'
import vue from 'eslint-plugin-vue'
import globals from 'globals'

export default defineConfigWithVueTs(
  // The locale files are checked by tools/i18n, not here.
  { ignores: ['dist/**', 'src/api/schema.d.ts', '**/*.json', '**/*.yaml'] },
  js.configs.recommended,
  // Correctness rules only: layout is left to the editor.
  vue.configs['flat/essential'],
  vueTsConfigs.recommendedTypeChecked,
  ...vueI18n.configs.recommended,
  {
    languageOptions: { globals: { ...globals.browser } },
    settings: {
      'vue-i18n': {
        localeDir: '../i18n/locales/*.json',
        messageSyntaxVersion: '^11.0.0',
      },
    },
    rules: {
      // Every user-facing text comes from the locale files (PLAN.md 7.4).
      '@intlify/vue-i18n/no-raw-text': ['error', { ignorePattern: '^[\\s\\-–—·:/()|•]+$' }],
      '@intlify/vue-i18n/no-missing-keys': 'error',
      '@intlify/vue-i18n/no-dynamic-keys': 'off', // API error codes are keys
      '@intlify/vue-i18n/no-unused-keys': 'off', // tools/i18n checks this for Go and TS together
      'vue/multi-word-component-names': 'off',
    },
  },
  // Node scripts: plain JavaScript, no type information.
  ...vueTsConfigs.disableTypeChecked.map((c) => ({ ...c, files: ['scripts/**/*.mjs', 'eslint.config.js'] })),
  {
    files: ['scripts/**/*.mjs', 'eslint.config.js'],
    languageOptions: { globals: { ...globals.node } },
  },
)
