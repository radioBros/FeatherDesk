<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * "Send test email" (FE-048): sends the current, unsaved settings to one
 * recipient and explains SMTP failures in plain words.
 */
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { useSessionStore } from '@/stores/session'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ config?: S['EmailSettings'] | null }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const session = useSessionStore()
const model = reactive({ to: session.me?.email ?? '' })
const result = ref<S['EmailTestResult'] | null>(null)
const toRules = [validations.required.email]

const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const onSend = submit(async () => {
  result.value = null
  result.value = await unwrap(
    api.POST('/admin/email/test', { body: { to: model.to.trim(), ...(props.config ? { config: props.config } : {}) } }),
  )
})
const failure = computed(() => {
  const r = result.value
  if (!r || r.ok) return null
  return t(`agent.email.testErrors.${r.error_code ?? 'other'}`)
})
</script>

<template>
  <v-form ref="formRef" data-testid="email-test-form" @submit.prevent="onSend">
    <p class="text-body-2 mb-3">{{ $t('agent.email.testHint') }}</p>
    <v-text-field v-model="model.to" :label="$t('agent.email.testRecipient')" :rules="toRules" :error-messages="fieldErrors.to" type="email" data-testid="email-test-to" />
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="email-test-error">{{ formError }}</v-alert>
    <v-alert v-if="result?.ok" type="success" variant="tonal" density="compact" class="mb-3" data-testid="email-test-ok">{{ $t('agent.email.testOk', { to: model.to }) }}</v-alert>
    <v-alert v-if="failure" type="error" variant="tonal" density="compact" class="mb-3" data-testid="email-test-failure">
      <div>{{ failure }}</div>
      <div v-if="result?.message" class="text-caption mt-1">{{ result.message }}</div>
    </v-alert>
    <div class="d-flex justify-end ga-2">
      <v-btn variant="text" @click="emit('close')">{{ $t('app.close') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="email-test-send">{{ $t('agent.email.send') }}</v-btn>
    </div>
  </v-form>
</template>
