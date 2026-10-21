<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Email settings form (FE-048): SMTP (write-only password, env-locked fields
 * read-only and left out of the body), sender identities, per-template
 * toggles and the triage mailbox. "Send test email" validates this same form
 * first and hands the unsaved config to the page (CONVENTIONS §12).
 */
import { computed, reactive } from 'vue'
import { mdiEyeOutline, mdiLockOutline } from '@mdi/js'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import PasswordWriteOnly from '../shared/PasswordWriteOnly.vue'
import EnvLockBadge from '../shared/EnvLockBadge.vue'
import { KNOWN_TEMPLATES } from './types'
import type { components } from '@/api/schema'

type S = components['schemas']
type Smtp = S['SmtpSettings']
const props = defineProps<{ settings?: S['EmailSettings'] | null }>()
const emit = defineEmits<{ saved: [S['EmailSettings']]; test: [S['EmailSettings']]; preview: [string] }>()

const src = props.settings
const locked = new Set(src?.locked_by_env ?? [])
const isLocked = (f: keyof Smtp) => locked.has(`smtp.${f}`) || locked.has(f)

const sender = (s?: S['SenderIdentity']) => ({ from_name: s?.from_name ?? '', from_address: s?.from_address ?? '', reply_to: s?.reply_to ?? '' })
const model = reactive({
  host: src?.smtp.host ?? '',
  port: src?.smtp.port === undefined ? '' : String(src.smtp.port),
  security: (src?.smtp.security ?? null) as S['SmtpSecurity'] | null,
  auth: (src?.smtp.auth ?? 'plain') as S['SmtpAuth'],
  username: src?.smtp.username ?? '',
  // Write-only: never pre-filled; sent only when the admin types a new one.
  password: '',
  helo: src?.smtp.helo ?? '',
  timeout_seconds: src?.smtp.timeout_seconds === undefined ? '' : String(src.smtp.timeout_seconds),
  allow_insecure_tls: src?.smtp.allow_insecure_tls ?? false,
  customer: sender(src?.senders.customer),
  staff: sender(src?.senders.staff),
  templates: { ...(src?.templates_enabled ?? {}) } as Record<string, boolean>,
  triage_mailbox: src?.triage_mailbox ?? '',
  plus_addressing: src?.plus_addressing ?? false,
})

const templateKeys = computed(() => {
  const keys = new Set<string>([...KNOWN_TEMPLATES, ...Object.keys(src?.templates_enabled ?? {})])
  return [...keys].sort()
})
const isAuthTemplate = (k: string) => k.startsWith('auth.')
const enabled = (k: string) => isAuthTemplate(k) || model.templates[k] !== false

const SECURITY: S['SmtpSecurity'][] = ['none', 'starttls', 'tls']
const AUTH: S['SmtpAuth'][] = ['none', 'plain', 'login', 'cram-md5']
const hostRules = [validations.required.any, validations.hostname]
const portRules = [validations.required.any, validations.port]
const securityRules = [validations.required.select]
const timeoutRules = [validations.integer, validations.range(1, 300)]
const nameRules = [validations.required.any, validations.maxLength(100)]
const addressRules = [validations.required.email]
const replyRules = [validations.email]
const triageRules = [validations.emailList]
const textRules = [validations.maxLength(255)]

/** The PUT/test body: env-locked SMTP fields and read-only fields are left out. */
function body(): S['EmailSettings'] {
  const smtp: Smtp = {}
  const put = <K extends keyof Smtp>(k: K, v: Smtp[K]) => {
    if (!isLocked(k)) smtp[k] = v
  }
  put('host', model.host.trim())
  put('port', Number(model.port))
  put('security', model.security ?? undefined)
  put('auth', model.auth)
  put('username', model.username.trim())
  if (model.password && !isLocked('password')) smtp.password = model.password
  put('helo', model.helo.trim() || undefined)
  if (model.timeout_seconds !== '') put('timeout_seconds', Number(model.timeout_seconds))
  put('allow_insecure_tls', model.allow_insecure_tls)
  const id = (s: { from_name: string; from_address: string; reply_to: string }): S['SenderIdentity'] => ({
    from_name: s.from_name.trim(),
    from_address: s.from_address.trim(),
    reply_to: s.reply_to.trim() || null,
  })
  const templates: Record<string, boolean> = {}
  for (const k of templateKeys.value) if (!isAuthTemplate(k)) templates[k] = model.templates[k] !== false
  return {
    smtp,
    senders: { customer: id(model.customer), staff: id(model.staff) },
    templates_enabled: templates,
    triage_mailbox: model.triage_mailbox.trim() || null,
    plus_addressing: model.plus_addressing,
  }
}

const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const onSave = submit(async () => {
  const saved = await unwrap(api.PUT('/admin/email', { body: body() }))
  model.password = ''
  emit('saved', saved)
})
const onTest = submit(() => emit('test', body()))
</script>

<template>
  <v-form ref="formRef" data-testid="email-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="email-form-error">{{ formError }}</v-alert>

    <section class="mb-6">
      <h2 class="text-subtitle-1 mb-1">{{ $t('agent.email.smtp') }}</h2>
      <p class="text-body-2 text-medium-emphasis mb-3">{{ $t('agent.email.smtpHint') }}</p>
      <v-row density="compact">
        <v-col cols="12" sm="8">
          <v-text-field v-model="model.host" :label="$t('agent.email.host')" :rules="hostRules" :disabled="isLocked('host')" :error-messages="fieldErrors['smtp.host']" data-testid="smtp-host">
            <template v-if="isLocked('host')" #append-inner><EnvLockBadge /></template>
          </v-text-field>
        </v-col>
        <v-col cols="12" sm="4">
          <v-text-field v-model="model.port" :label="$t('agent.email.port')" inputmode="numeric" :rules="portRules" :disabled="isLocked('port')" :error-messages="fieldErrors['smtp.port']" data-testid="smtp-port">
            <template v-if="isLocked('port')" #append-inner><EnvLockBadge /></template>
          </v-text-field>
        </v-col>
        <v-col cols="12" sm="6">
          <v-select
            v-model="model.security"
            :items="SECURITY.map((v) => ({ value: v, title: $t(`agent.email.securityOptions.${v}`) }))"
            :label="$t('agent.email.security')"
            :rules="securityRules"
            :disabled="isLocked('security')"
            :error-messages="fieldErrors['smtp.security']"
            data-testid="smtp-security"
          >
            <template v-if="isLocked('security')" #append-inner><EnvLockBadge /></template>
          </v-select>
        </v-col>
        <v-col cols="12" sm="6">
          <v-select
            v-model="model.auth"
            :items="AUTH.map((v) => ({ value: v, title: $t(`agent.email.authOptions.${v}`) }))"
            :label="$t('agent.email.auth')"
            :disabled="isLocked('auth')"
            data-testid="smtp-auth"
          >
            <template v-if="isLocked('auth')" #append-inner><EnvLockBadge /></template>
          </v-select>
        </v-col>
        <template v-if="model.auth !== 'none'">
          <v-col cols="12" sm="6">
            <v-text-field v-model="model.username" :label="$t('agent.email.username')" :rules="textRules" :disabled="isLocked('username')" autocomplete="off" data-testid="smtp-username">
              <template v-if="isLocked('username')" #append-inner><EnvLockBadge /></template>
            </v-text-field>
          </v-col>
          <v-col cols="12" sm="6">
            <PasswordWriteOnly
              v-model="model.password"
              :label="$t('agent.email.password')"
              :password-set="!!src?.smtp.password_set"
              :locked="isLocked('password')"
              testid="smtp-password"
            />
          </v-col>
        </template>
        <v-col cols="12" sm="6">
          <v-text-field v-model="model.helo" :label="$t('agent.email.helo')" :rules="[validations.hostname]" :disabled="isLocked('helo')" :hint="$t('agent.email.heloHint')">
            <template v-if="isLocked('helo')" #append-inner><EnvLockBadge /></template>
          </v-text-field>
        </v-col>
        <v-col cols="12" sm="6">
          <v-text-field v-model="model.timeout_seconds" :label="$t('agent.email.timeout')" inputmode="numeric" :rules="timeoutRules" :disabled="isLocked('timeout_seconds')" data-testid="smtp-timeout">
            <template v-if="isLocked('timeout_seconds')" #append-inner><EnvLockBadge /></template>
          </v-text-field>
        </v-col>
      </v-row>
      <v-switch v-model="model.allow_insecure_tls" :label="$t('agent.email.insecureTls')" :disabled="isLocked('allow_insecure_tls')" color="warning" inset hide-details data-testid="smtp-insecure" />
      <v-alert v-if="model.allow_insecure_tls" type="warning" variant="tonal" density="compact" class="mt-2" data-testid="insecure-warning">
        {{ $t('agent.email.insecureWarning') }}
      </v-alert>
    </section>

    <section class="mb-6">
      <h2 class="text-subtitle-1 mb-3">{{ $t('agent.email.senders') }}</h2>
      <v-row density="compact">
        <v-col v-for="who in ['customer', 'staff'] as const" :key="who" cols="12" md="6">
          <div class="text-subtitle-2 mb-2">{{ $t(`agent.email.sender.${who}`) }}</div>
          <v-text-field v-model="model[who].from_name" :label="$t('agent.email.fromName')" :rules="nameRules" :error-messages="fieldErrors[`senders.${who}.from_name`]" :data-testid="`sender-${who}-name`" />
          <v-text-field v-model="model[who].from_address" :label="$t('agent.email.fromAddress')" :rules="addressRules" :error-messages="fieldErrors[`senders.${who}.from_address`]" type="email" :data-testid="`sender-${who}-address`" />
          <v-text-field v-model="model[who].reply_to" :label="$t('agent.email.replyTo')" :rules="replyRules" :error-messages="fieldErrors[`senders.${who}.reply_to`]" type="email" />
        </v-col>
      </v-row>
      <v-switch v-model="model.plus_addressing" :label="$t('agent.email.plusAddressing')" :hint="$t('agent.email.plusAddressingHint')" persistent-hint color="primary" inset />
    </section>

    <section class="mb-6">
      <h2 class="text-subtitle-1 mb-1">{{ $t('agent.email.triage') }}</h2>
      <p class="text-body-2 text-medium-emphasis mb-3">{{ $t('agent.email.triageHint') }}</p>
      <v-text-field v-model="model.triage_mailbox" :label="$t('agent.email.triageMailbox')" :rules="triageRules" :error-messages="fieldErrors.triage_mailbox" data-testid="triage-mailbox" />
    </section>

    <section class="mb-6">
      <h2 class="text-subtitle-1 mb-1">{{ $t('agent.email.templates') }}</h2>
      <p class="text-body-2 text-medium-emphasis mb-2">{{ $t('agent.email.templatesHint') }}</p>
      <v-list density="compact" border rounded data-testid="template-list">
        <v-list-item v-for="k in templateKeys" :key="k" :data-testid="`template-${k}`">
          <v-list-item-title><code>{{ k }}</code></v-list-item-title>
          <template #append>
            <v-icon v-if="isAuthTemplate(k)" :icon="mdiLockOutline" size="small" class="mr-2" :title="$t('agent.email.alwaysOn')" />
            <v-switch
              :model-value="enabled(k)"
              :disabled="isAuthTemplate(k)"
              :aria-label="k"
              color="primary"
              density="compact"
              inset
              hide-details
              class="mr-2"
              :data-testid="`template-toggle-${k}`"
              @update:model-value="(v: boolean | null) => (model.templates[k] = !!v)"
            />
            <v-btn :icon="mdiEyeOutline" variant="text" size="small" :aria-label="$t('agent.common.preview')" :data-testid="`template-preview-${k}`" @click="emit('preview', k)" />
          </template>
        </v-list-item>
      </v-list>
    </section>

    <div class="d-flex flex-wrap justify-end ga-2">
      <v-btn variant="tonal" :loading="submitting" data-testid="email-test" @click="onTest">{{ $t('agent.email.sendTest') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="email-save">{{ $t('agent.common.saveChanges') }}</v-btn>
    </div>
  </v-form>
</template>
