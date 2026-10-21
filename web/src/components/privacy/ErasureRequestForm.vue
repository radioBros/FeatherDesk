<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Customer account-deletion request (FE-082, D-19 §C): an optional note for the
 * admin plus an explicit "I understand" checkbox. Emits `requested` with the
 * new pending request after the 202.
 */
import { reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'

const emit = defineEmits<{ requested: [components['schemas']['PrivacyRequest']] }>()
const model = reactive({ reason: '', confirm: false })
const reasonRules = [validations.maxLength(1000)]
const confirmRules = [validations.required.any]
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSubmit = submit(async () => {
  const reason = model.reason.trim()
  const req = await unwrap(api.POST('/me/privacy/erasure', { body: reason ? { reason } : {} }))
  emit('requested', req)
})
</script>

<template>
  <v-form ref="formRef" data-testid="erasure-request-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-textarea
      v-model="model.reason"
      :label="$t('privacy.my.reason')"
      :rules="reasonRules"
      :error-messages="fieldErrors.reason"
      rows="2"
      auto-grow
      counter="1000"
      data-testid="erasure-reason"
    />
    <v-checkbox v-model="model.confirm" :label="$t('privacy.my.confirm')" :rules="confirmRules" data-testid="erasure-confirm" />
    <v-btn type="submit" color="error" variant="tonal" :loading="submitting" data-testid="erasure-submit">{{ $t('privacy.my.deletionButton') }}</v-btn>
  </v-form>
</template>
