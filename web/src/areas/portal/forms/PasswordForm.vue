<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import PasswordField from '@/components/PasswordField.vue'
import { reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { useUiStore } from '@/stores/ui'

/** Change password, local accounts only (FE-018). */
const ui = useUiStore()
const { t } = useI18n()
const model = reactive({ current: '', next: '', confirm: '' })
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSubmit = submit(async () => {
  await unwrap(api.PATCH('/me', { body: { current_password: model.current, new_password: model.next } }))
  model.current = model.next = model.confirm = ''
  formRef.value?.resetValidation?.()
  ui.notify(t('portal.profile.passwordChanged'), 'success')
})
</script>

<template>
  <v-form ref="formRef" data-testid="password-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="form-error">{{ formError }}</v-alert>
    <PasswordField
      v-model="model.current"
      :label="$t('portal.profile.currentPassword')"
      :rules="[validations.required.any]"
      :error-messages="fieldErrors.current_password"
      autocomplete="current-password"
      data-testid="pw-current"
    />
    <PasswordField
      v-model="model.next"
      :label="$t('portal.account.newPassword')"
      :rules="[validations.required.any, validations.password]"
      :error-messages="fieldErrors.new_password"
      :hint="$t('portal.account.passwordHint')"
      autocomplete="new-password"
      data-testid="pw-new"
    />
    <PasswordField
      v-model="model.confirm"
      :label="$t('portal.account.confirmPassword')"
      :rules="[validations.required.any, validations.sameAs(() => model.next)]"
      autocomplete="new-password"
      data-testid="pw-confirm"
    />
    <v-btn type="submit" color="primary" variant="tonal" :loading="submitting" data-testid="pw-submit">{{ $t('portal.profile.changePassword') }}</v-btn>
  </v-form>
</template>
