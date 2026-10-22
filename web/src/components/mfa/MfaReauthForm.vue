<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import PasswordField from '@/components/PasswordField.vue'
/** Password confirmation before any MFA change (FE-072): opens a 10-minute window on this session. */
import { ref } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { MfaStatus } from './mfa'

const emit = defineEmits<{ done: [status: MfaStatus]; cancel: [] }>()
const password = ref('')
const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const onSubmit = submit(async () => {
  emit('done', await unwrap(api.POST('/me/mfa/reauth', { body: { password: password.value } })))
})
</script>

<template>
  <v-form ref="formRef" data-testid="mfa-reauth-form" @submit.prevent="onSubmit">
    <p class="text-body-2 mb-3">{{ $t('mfa.reauth.intro') }}</p>
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="form-error">{{ formError }}</v-alert>
    <PasswordField
      v-model="password"
      :label="$t('auth.password')"
      :rules="[validations.required.any]"
      :error-messages="fieldErrors.password"
      autocomplete="current-password"
      autofocus
      data-testid="mfa-reauth-password"
    />
    <div class="d-flex justify-end ga-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="mfa-reauth-submit">{{ $t('app.confirm') }}</v-btn>
    </div>
  </v-form>
</template>
