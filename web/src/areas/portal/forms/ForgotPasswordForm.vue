<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { ref } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'

/**
 * Password recovery request (FE-014). The API always answers 202, so the page
 * shows the same "if an account exists…" message whatever the email was.
 */
const emit = defineEmits<{ sent: [] }>()
const email = ref('')
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSubmit = submit(async () => {
  await unwrap(api.POST('/auth/password/forgot', { body: { email: email.value.trim() } }))
  emit('sent')
})
</script>

<template>
  <v-form ref="formRef" data-testid="forgot-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="form-error">{{ formError }}</v-alert>
    <v-text-field
      v-model="email"
      :label="$t('auth.email')"
      :rules="[validations.required.email]"
      :error-messages="fieldErrors.email"
      type="email"
      autocomplete="email"
      data-testid="forgot-email"
    />
    <v-btn type="submit" color="primary" block :loading="submitting" data-testid="forgot-submit">{{ $t('portal.account.sendResetLink') }}</v-btn>
  </v-form>
</template>
