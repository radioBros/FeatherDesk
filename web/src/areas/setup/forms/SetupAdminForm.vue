<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import PasswordField from '@/components/PasswordField.vue'
import { reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'

/** Step 2: the first administrator (break-glass local account). The password lives in memory only. */
const props = defineProps<{ name?: string; email?: string; errors?: Record<string, string[]>; notice?: string | null }>()
const emit = defineEmits<{ next: [admin: { name: string; email: string; password: string }]; back: [] }>()
const model = reactive({ name: props.name ?? '', email: props.email ?? '', password: '', confirm: '' })
const { formRef, submit, fieldErrors, submitting } = useForm()

const onSubmit = submit(() => emit('next', { name: model.name.trim(), email: model.email.trim(), password: model.password }))
</script>

<template>
  <v-form ref="formRef" data-testid="setup-admin-form" @submit.prevent="onSubmit">
    <v-alert v-if="notice" type="info" variant="tonal" density="compact" class="mb-3">{{ notice }}</v-alert>
    <v-text-field
      v-model="model.name"
      :label="$t('setup.admin.name')"
      :rules="[validations.required.any, validations.maxLength(120)]"
      :error-messages="fieldErrors.name ?? errors?.['admin.name']"
      autocomplete="name"
      data-testid="setup-admin-name"
    />
    <v-text-field
      v-model="model.email"
      :label="$t('auth.email')"
      :rules="[validations.required.email]"
      :error-messages="fieldErrors.email ?? errors?.['admin.email']"
      type="email"
      autocomplete="email"
      data-testid="setup-admin-email"
    />
    <PasswordField
      v-model="model.password"
      :label="$t('auth.password')"
      :rules="[validations.required.any, validations.password]"
      :error-messages="fieldErrors.password ?? errors?.['admin.password']"
      :hint="$t('setup.admin.passwordHint')"
      autocomplete="new-password"
      data-testid="setup-admin-password"
    />
    <PasswordField
      v-model="model.confirm"
      :label="$t('setup.admin.confirmPassword')"
      :rules="[validations.required.any, validations.sameAs(() => model.password)]"
      autocomplete="new-password"
      data-testid="setup-admin-confirm"
    />
    <div class="d-flex justify-space-between">
      <v-btn variant="text" @click="emit('back')">{{ $t('app.back') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="setup-next">{{ $t('setup.next') }}</v-btn>
    </div>
  </v-form>
</template>
