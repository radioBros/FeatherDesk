<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { ref } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { apiFetch } from '@/api/client'

/** Passwordless sign-in request (FE-034): `{email}` sends a one-time link. */
const emit = defineEmits<{ sent: [] }>()
const email = ref('')
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSubmit = submit(async () => {
  // MagicLinkRequest is a oneOf; plain fetch keeps the call simple.
  await apiFetch('/auth/magic-link', { method: 'POST', body: JSON.stringify({ email: email.value.trim() }) })
  emit('sent')
})
</script>

<template>
  <v-form ref="formRef" data-testid="magic-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="form-error">{{ formError }}</v-alert>
    <v-text-field
      v-model="email"
      :label="$t('auth.email')"
      :rules="[validations.required.email]"
      :error-messages="fieldErrors.email"
      type="email"
      autocomplete="email"
      data-testid="magic-email"
    />
    <v-btn type="submit" variant="outlined" block :loading="submitting" data-testid="magic-submit">{{ $t('portal.account.sendMagicLink') }}</v-btn>
  </v-form>
</template>
