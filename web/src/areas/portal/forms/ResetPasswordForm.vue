<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import PasswordField from '@/components/PasswordField.vue'
import { reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'

/** New password from an emailed reset link (FE-014). */
const props = defineProps<{ token: string }>()
const emit = defineEmits<{ done: []; expired: [message: string] }>()
const model = reactive({ password: '', confirm: '' })
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSubmit = submit(async () => {
  try {
    await unwrap(api.POST('/auth/password/reset', { body: { token: props.token, password: model.password } }))
  } catch (e) {
    // An expired or used token is a 400: the page offers a new link instead of a field error.
    if (e instanceof ApiProblem && e.status === 400) {
      emit('expired', e.detail || e.title || '')
      return
    }
    throw e
  }
  emit('done')
})
</script>

<template>
  <v-form ref="formRef" data-testid="reset-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="form-error">{{ formError }}</v-alert>
    <PasswordField
      v-model="model.password"
      :label="$t('portal.account.newPassword')"
      :rules="[validations.required.any, validations.password]"
      :error-messages="fieldErrors.password"
      :hint="$t('portal.account.passwordHint')"
      autocomplete="new-password"
      data-testid="reset-password"
    />
    <PasswordField
      v-model="model.confirm"
      :label="$t('portal.account.confirmPassword')"
      :rules="[validations.required.any, validations.sameAs(() => model.password)]"
      autocomplete="new-password"
      data-testid="reset-confirm"
    />
    <v-btn type="submit" color="primary" block :loading="submitting" data-testid="reset-submit">{{ $t('portal.account.setPassword') }}</v-btn>
  </v-form>
</template>
