<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Erase a user's personal data (FE-085, D-19 §C), admin only. With `requestId`
 * it approves that pending deletion request, else it erases `userId` directly.
 * The mode has no default and the irreversibility must be acknowledged, so an
 * accidental click never erases anyone.
 */
import { computed, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ userId?: string | null; requestId?: string | null }>()
const emit = defineEmits<{ erased: [S['ErasureResult']]; cancel: [] }>()
const { t } = useI18n()
const model = reactive({ mode: null as S['ErasureMode'] | null, confirm: false })
const modeRules = [validations.required.select]
const confirmRules = [validations.required.any]
const MODES: S['ErasureMode'][] = ['anonymize', 'anonymize_and_redact']
const modes = computed(() => MODES.map((m) => ({ value: m, title: t(`privacy.mode.${m}`) })))
const hint = computed(() =>
  model.mode === 'anonymize_and_redact' ? t('privacy.mode.hintRedact') : model.mode === 'anonymize' ? t('privacy.mode.hintAnonymize') : '',
)

const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const onSubmit = submit(async () => {
  const body = { mode: model.mode as S['ErasureMode'] }
  const result = props.requestId
    ? await unwrap(api.POST('/admin/privacy/requests/{id}/approve', { params: { path: { id: props.requestId } }, body }))
    : await unwrap(api.POST('/admin/privacy/users/{id}/erase', { params: { path: { id: props.userId ?? '' } }, body }))
  emit('erased', result)
})
</script>

<template>
  <v-form ref="formRef" data-testid="erase-form" @submit.prevent="onSubmit">
    <v-alert type="warning" variant="tonal" density="compact" class="mb-3">{{ $t('privacy.erase.intro') }}</v-alert>
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="erase-error">{{ formError }}</v-alert>
    <v-select
      v-model="model.mode"
      :items="modes"
      :label="$t('privacy.mode.label')"
      :rules="modeRules"
      :error-messages="fieldErrors.mode"
      :hint="hint"
      persistent-hint
      data-testid="erase-mode"
    />
    <v-checkbox v-model="model.confirm" :label="$t('privacy.erase.confirm')" :rules="confirmRules" class="mt-2" data-testid="erase-confirm" />
    <div class="d-flex justify-end ga-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('privacy.action.cancel') }}</v-btn>
      <v-btn type="submit" color="error" :loading="submitting" data-testid="erase-submit">
        {{ requestId ? $t('privacy.action.approve') : $t('privacy.action.erase') }}
      </v-btn>
    </div>
  </v-form>
</template>
