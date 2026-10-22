<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * One code field (FE-070): a 6-digit TOTP / e-mail code or a recovery code.
 * The caller passes the request as `action`; a 422 on `code` lands on the field.
 */
import { computed, ref } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'

const props = defineProps<{
  kind: 'totp' | 'email' | 'recovery'
  action: (code: string) => Promise<unknown>
  submitLabel?: string
}>()
const code = ref('')
const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const codeRules = computed(() =>
  props.kind === 'recovery' ? [validations.required.any, validations.recoveryCode] : [validations.required.any, validations.otpCode],
)
const onSubmit = submit(async () => {
  await props.action(code.value.trim())
})
</script>

<template>
  <v-form ref="formRef" data-testid="mfa-code-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="form-error">{{ formError }}</v-alert>
    <v-text-field
      v-model="code"
      :label="kind === 'recovery' ? $t('mfa.recoveryCode') : $t('mfa.code')"
      :rules="codeRules"
      :error-messages="fieldErrors.code"
      :inputmode="kind === 'recovery' ? 'text' : 'numeric'"
      autocomplete="one-time-code"
      autofocus
      data-testid="mfa-code"
    />
    <v-btn type="submit" color="primary" block :loading="submitting" data-testid="mfa-code-submit">{{ submitLabel || $t('mfa.verify') }}</v-btn>
  </v-form>
</template>
