<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import PasswordField from '@/components/PasswordField.vue'
/** Change the local password (only shown when `me.has_password`). Other sessions are revoked by the server. */
import { reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'

const emit = defineEmits<{ saved: [] }>()
const { t } = useI18n()
const model = reactive({ current: '', next: '', confirm: '' })
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSave = submit(async () => {
  if (model.next !== model.confirm) {
    // Cross-field check: no request while the confirmation differs.
    fieldErrors.confirm = [t('agent.profile.passwordMismatch')]
    return
  }
  await unwrap(api.PATCH('/me', { body: { current_password: model.current, new_password: model.next } }))
  model.current = model.next = model.confirm = ''
  formRef.value?.resetValidation?.()
  emit('saved')
})
</script>

<template>
  <v-form ref="formRef" data-testid="password-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <PasswordField
      v-model="model.current"
      :label="$t('agent.profile.currentPassword')"
      autocomplete="current-password"
      :rules="[validations.required.any]"
      :error-messages="fieldErrors.current_password"
      data-testid="password-current"
    />
    <PasswordField
      v-model="model.next"
      :label="$t('agent.profile.newPassword')"
      autocomplete="new-password"
      :rules="[validations.required.any, validations.password]"
      :error-messages="fieldErrors.new_password"
      data-testid="password-new"
    />
    <PasswordField
      v-model="model.confirm"
      :label="$t('agent.profile.confirmPassword')"
      autocomplete="new-password"
      :rules="[validations.required.any]"
      :error-messages="fieldErrors.confirm"
      data-testid="password-confirm"
      @update:model-value="delete fieldErrors.confirm"
    />
    <div class="d-flex justify-end">
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="password-save">{{ $t('agent.profile.changePassword') }}</v-btn>
    </div>
  </v-form>
</template>
