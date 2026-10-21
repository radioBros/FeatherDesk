// SPDX-License-Identifier: AGPL-3.0-only

import { RuleTester } from 'eslint'
import vueParser from 'vue-eslint-parser'
import tseslint from 'typescript-eslint'
import local from './index.js'

RuleTester.describe = describe
RuleTester.it = it
RuleTester.itOnly = it.only

const tester = new RuleTester({
  languageOptions: {
    parser: vueParser,
    parserOptions: { parser: tseslint.parser, ecmaVersion: 'latest', sourceType: 'module' },
  },
})

const sfc = (template: string, script = '') =>
  `<script setup lang="ts">\n${script}\n</script>\n<template>${template}</template>`

tester.run('rules-from-validations', local.rules['rules-from-validations'] as never, {
  valid: [
    { code: sfc('<v-text-field :rules="[validations.required.email]" />'), filename: 'A.vue' },
    { code: sfc('<v-text-field :rules="[validations.required.any, validations.maxLength(10)]" />'), filename: 'A.vue' },
    { code: sfc('<v-select :rules="nameRules" />', 'const nameRules = [validations.required.any]'), filename: 'A.vue' },
    { code: sfc('<v-text-field :rules="validations.forField(def)" />'), filename: 'A.vue' },
  ],
  invalid: [
    { code: sfc('<v-text-field :rules="[(v) => !!v || \'required\']" />'), filename: 'A.vue', errors: [{ messageId: 'inline' }] },
    { code: sfc('<v-textarea :rules="[myCheck]" />'), filename: 'A.vue', errors: [{ messageId: 'inline' }] },
    {
      code: sfc('<v-text-field :rules="emailRules" />', 'const emailRules = [(v: string) => !!v]'),
      filename: 'A.vue',
      errors: [{ messageId: 'inlineScript' }],
    },
  ],
})

tester.run('form-submit-validates', local.rules['form-submit-validates'] as never, {
  valid: [
    {
      code: sfc('<v-form ref="formRef" @submit.prevent="onSave" />', 'const { submit } = useForm(); const onSave = submit(async () => {})'),
      filename: 'A.vue',
    },
    {
      code: sfc('<v-form ref="f" @submit.prevent="save" />', 'async function save() { const r = await f.value.validate(); if (!r.valid) return }'),
      filename: 'A.vue',
    },
  ],
  invalid: [
    { code: sfc('<v-form />'), filename: 'A.vue', errors: [{ messageId: 'missing' }] },
    {
      code: sfc('<v-form @submit.prevent="save" />', 'async function save() { await api.post() }'),
      filename: 'A.vue',
      errors: [{ messageId: 'bypass' }],
    },
  ],
})
