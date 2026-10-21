<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import PasswordField from '@/components/PasswordField.vue'
import { reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { useSessionStore } from '@/stores/session'
import type { Me } from '@/types'
import { isMfaChallenge, type MfaChallenge } from '@/components/mfa/mfa'

/** Accept an organization invitation (FE-034): name + password, then signed in. */
const props = defineProps<{ token: string }>()
const emit = defineEmits<{ accepted: []; mfa: [challenge: MfaChallenge] }>()
const session = useSessionStore()
const model = reactive({ name: '', password: '', confirm: '' })
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSubmit = submit(async () => {
  const me = await unwrap(
    api.POST('/auth/invite/accept', { body: { token: props.token, name: model.name.trim(), password: model.password } }),
  )
  if (isMfaChallenge(me)) {
    emit('mfa', me) // the policy requires a second factor before the first session (D-19 §B)
    return
  }
  session.set(me as unknown as Me)
  emit('accepted')
})
</script>

<template>
  <v-form ref="formRef" data-testid="invite-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="form-error">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.name"
      :label="$t('portal.account.name')"
      :rules="[validations.required.any, validations.maxLength(120)]"
      :error-messages="fieldErrors.name"
      autocomplete="name"
      data-testid="invite-name"
    />
    <PasswordField
      v-model="model.password"
      :label="$t('auth.password')"
      :rules="[validations.required.any, validations.password]"
      :error-messages="fieldErrors.password"
      :hint="$t('portal.account.passwordHint')"
      autocomplete="new-password"
      data-testid="invite-password"
    />
    <PasswordField
      v-model="model.confirm"
      :label="$t('portal.account.confirmPassword')"
      :rules="[validations.required.any, validations.sameAs(() => model.password)]"
      autocomplete="new-password"
      data-testid="invite-confirm"
    />
    <v-btn type="submit" color="primary" block :loading="submitting" data-testid="invite-submit">{{ $t('portal.account.acceptInvite') }}</v-btn>
  </v-form>
</template>
