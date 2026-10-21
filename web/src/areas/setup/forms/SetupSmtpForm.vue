<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import PasswordField from '@/components/PasswordField.vue'
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { apiFetch } from '@/api/client'
import type { SetupState } from '../state'

type Smtp = SetupState['smtp']

/**
 * Step 4 (optional): outgoing email. "Send test email" and "Save" both run the
 * form's validate() first (useForm.submit). Runs with the admin session that
 * POST /setup/complete created. The password is never persisted.
 */
const props = defineProps<{ initial?: Partial<Smtp>; adminEmail?: string }>()
const emit = defineEmits<{ saved: [smtp: Smtp]; skip: [] }>()
const { t } = useI18n()
const model = reactive<Smtp & { password: string; testTo: string }>({
  host: props.initial?.host ?? '',
  port: props.initial?.port ?? 587,
  security: props.initial?.security ?? 'starttls',
  username: props.initial?.username ?? '',
  from_name: props.initial?.from_name ?? '',
  from_address: props.initial?.from_address ?? '',
  password: '',
  testTo: props.adminEmail ?? '',
})
const securityItems = computed(() => [
  { value: 'starttls', title: t('setup.smtp.starttls') },
  { value: 'tls', title: t('setup.smtp.tls') },
  { value: 'none', title: t('setup.smtp.none') },
])
const testResult = ref<{ ok: boolean; message: string } | null>(null)
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

function settings() {
  const sender = { from_name: model.from_name.trim() || undefined, from_address: model.from_address.trim() }
  return {
    smtp: {
      host: model.host.trim(),
      port: Number(model.port),
      security: model.security,
      username: model.username.trim() || undefined,
      ...(model.password ? { password: model.password } : {}),
      auth: model.username.trim() ? 'plain' : 'none',
    },
    senders: { customer: sender, staff: sender },
  }
}

function plain(): Smtp {
  const { host, port, security, username, from_name, from_address } = model
  return { host, port, security, username, from_name, from_address }
}

const onTest = submit(async () => {
  testResult.value = null
  const res = await apiFetch<{ ok: boolean; error_code?: string | null; message?: string }>('/admin/email/test', {
    method: 'POST',
    body: JSON.stringify({ to: model.testTo.trim(), config: settings() }),
  })
  testResult.value = res.ok
    ? { ok: true, message: t('setup.smtp.testOk', { to: model.testTo.trim() }) }
    : { ok: false, message: res.message || t(`setup.smtp.error.${res.error_code ?? 'other'}`) }
})

const onSubmit = submit(async () => {
  await apiFetch('/admin/email', { method: 'PUT', body: JSON.stringify(settings()) })
  emit('saved', plain())
})
</script>

<template>
  <v-form ref="formRef" data-testid="setup-smtp-form" @submit.prevent="onSubmit">
    <p class="text-body-2 mb-4">{{ $t('setup.smtp.intro') }}</p>
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="form-error">{{ formError }}</v-alert>
    <v-row density="compact">
      <v-col cols="12" sm="8">
        <v-text-field
          v-model="model.host"
          :label="$t('setup.smtp.host')"
          :rules="[validations.required.any, validations.hostname]"
          :error-messages="fieldErrors['smtp.host']"
          data-testid="smtp-host"
        />
      </v-col>
      <v-col cols="12" sm="4">
        <v-text-field
          v-model="model.port"
          :label="$t('setup.smtp.port')"
          :rules="[validations.required.any, validations.port]"
          :error-messages="fieldErrors['smtp.port']"
          type="number"
          data-testid="smtp-port"
        />
      </v-col>
    </v-row>
    <v-select
      v-model="model.security"
      :items="securityItems"
      :label="$t('setup.smtp.security')"
      :rules="[validations.required.select]"
      :error-messages="fieldErrors['smtp.security']"
      data-testid="smtp-security"
    />
    <v-row density="compact">
      <v-col cols="12" sm="6">
        <v-text-field v-model="model.username" :label="$t('setup.smtp.username')" autocomplete="off" :error-messages="fieldErrors['smtp.username']" data-testid="smtp-username" />
      </v-col>
      <v-col cols="12" sm="6">
        <PasswordField
          v-model="model.password"
          :label="$t('setup.smtp.password')"
          autocomplete="new-password"
          :error-messages="fieldErrors['smtp.password']"
          data-testid="smtp-password"
        />
      </v-col>
    </v-row>
    <v-row density="compact">
      <v-col cols="12" sm="6">
        <v-text-field
          v-model="model.from_address"
          :label="$t('setup.smtp.fromAddress')"
          :rules="[validations.required.email]"
          :error-messages="fieldErrors['senders.customer.from_address']"
          type="email"
          data-testid="smtp-from"
        />
      </v-col>
      <v-col cols="12" sm="6">
        <v-text-field v-model="model.from_name" :label="$t('setup.smtp.fromName')" :rules="[validations.maxLength(100)]" data-testid="smtp-from-name" />
      </v-col>
    </v-row>
    <v-text-field
      v-model="model.testTo"
      :label="$t('setup.smtp.testTo')"
      :rules="[validations.required.email]"
      :error-messages="fieldErrors.to"
      type="email"
      data-testid="smtp-test-to"
    />
    <v-alert
      v-if="testResult"
      :type="testResult.ok ? 'success' : 'error'"
      variant="tonal"
      density="compact"
      class="mb-3"
      data-testid="smtp-test-result"
      role="status"
    >
      {{ testResult.message }}
    </v-alert>
    <div class="d-flex flex-wrap justify-space-between ga-2">
      <v-btn variant="text" :disabled="submitting" data-testid="smtp-skip" @click="emit('skip')">{{ $t('setup.smtp.skip') }}</v-btn>
      <div class="d-flex ga-2">
        <v-btn variant="outlined" :loading="submitting" data-testid="smtp-test" @click="onTest()">{{ $t('setup.smtp.test') }}</v-btn>
        <v-btn type="submit" color="primary" :loading="submitting" data-testid="setup-next">{{ $t('setup.smtp.save') }}</v-btn>
      </div>
    </div>
  </v-form>
</template>
