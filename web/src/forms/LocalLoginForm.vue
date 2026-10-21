<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import PasswordField from '@/components/PasswordField.vue'
import { reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { apiFetch } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { t } from '@/i18n'
import { useSessionStore } from '@/stores/session'
import { isMfaChallenge, type MfaChallenge } from '@/components/mfa/mfa'

const props = defineProps<{ audience: 'staff' | 'customer' }>()
/** `mfa`: the password was right and a second step is needed (D-19 §B); the page shows MfaStep. */
const emit = defineEmits<{ success: []; mfa: [challenge: MfaChallenge] }>()
const session = useSessionStore()
const model = reactive({ email: '', password: '' })
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSubmit = submit(async () => {
  let res: unknown
  try {
    res = await apiFetch<unknown>('/auth/login', {
      method: 'POST',
      body: JSON.stringify({ audience: props.audience, email: model.email.trim(), password: model.password }),
    })
  } catch (e) {
    // Generic text on bad credentials: never hint whether the account exists (FE-013).
    if (e instanceof ApiProblem && e.status === 401) {
      formError.value = t('auth.invalidCredentials')
      return
    }
    throw e
  }
  if (isMfaChallenge(res)) {
    emit('mfa', res)
    return
  }
  await session.refresh()
  emit('success')
})
</script>

<template>
  <v-form ref="formRef" data-testid="login-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="form-error">
      {{ formError }}
    </v-alert>
    <v-text-field
      v-model="model.email"
      :label="$t('auth.email')"
      :rules="[validations.required.email]"
      :error-messages="fieldErrors.email"
      type="email"
      autocomplete="username"
      data-testid="login-email"
    />
    <PasswordField
      v-model="model.password"
      :label="$t('auth.password')"
      :rules="[validations.required.any]"
      :error-messages="fieldErrors.password"
      autocomplete="current-password"
      data-testid="login-password"
    />
    <v-btn type="submit" color="primary" block :loading="submitting" data-testid="login-submit">{{ $t('auth.signIn') }}</v-btn>
  </v-form>
</template>
