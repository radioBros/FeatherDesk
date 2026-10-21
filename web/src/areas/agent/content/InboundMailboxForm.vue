<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import PasswordField from '@/components/PasswordField.vue'
/**
 * Inbound mailbox form (FE-057, D-16). The IMAP password is write-only (never
 * pre-filled, sent only when typed); env-locked fields are read-only, badged
 * and left out of the body. Save **and** "Test connection" both run through
 * `useForm().submit`, so `validate()` always runs first (CONVENTIONS §12).
 */
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import PasswordWriteOnly from '../shared/PasswordWriteOnly.vue'
import EnvLockBadge from '../shared/EnvLockBadge.vue'
import { useForm } from '@/composables/useForm'
import { validations, type Rule } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'

type S = components['schemas']
type Mailbox = S['InboundMailbox']

const props = withDefaults(defineProps<{ mailbox?: Mailbox | null; teams?: S['Team'][] }>(), { mailbox: null, teams: () => [] })
const emit = defineEmits<{ saved: [Mailbox, boolean]; cancel: [] }>()
const { t } = useI18n()

const m = props.mailbox
const model = reactive({
  name: m?.name ?? '',
  kind: (m?.kind ?? null) as Mailbox['kind'] | null,
  team_id: m?.team_id ?? (null as string | null),
  address: m?.address ?? '',
  active: m?.active ?? true,
  host: m?.imap?.host ?? '',
  port: m?.imap?.port !== undefined ? String(m.imap.port) : '993',
  security: (m?.imap?.security ?? 'tls') as S['SmtpSecurity'] | null,
  auth: (m?.imap?.auth ?? 'password') as 'password' | 'xoauth2' | null,
  username: m?.imap?.username ?? '',
  password: '',
  folder: m?.imap?.folder ?? 'INBOX',
  processed_action: (m?.imap?.processed_action ?? 'move') as 'move' | 'flag' | null,
  processed_folder: m?.imap?.processed_folder ?? '',
  webhook_hmac_secret: '',
})
const testResult = ref<S['InboundTestResult'] | null>(null)

const locked = (path: string) => !!props.mailbox?.locked_by_env?.includes(path)
/** Locked fields keep their server value and carry no client rules. */
const rulesFor = (path: string, rules: Rule[]) => (locked(path) ? [] : rules)
const hostRules = computed(() => rulesFor('imap.host', [validations.required.any, validations.hostname]))
const portRules = computed(() => rulesFor('imap.port', [validations.required.any, validations.port]))
const usernameRules = computed(() => rulesFor('imap.username', [validations.required.any]))
const folderRules = computed(() => rulesFor('imap.folder', [validations.required.any]))
const securityRules = computed(() => rulesFor('imap.security', [validations.required.select]))
const authRules = computed(() => rulesFor('imap.auth', [validations.required.select]))
const actionRules = computed(() => rulesFor('imap.processed_action', [validations.required.select]))
const nameRules = computed(() => rulesFor('name', [validations.required.any, validations.maxLength(100)]))
const addressRules = computed(() => rulesFor('address', [validations.required.email]))

const kindItems = computed(() => (['imap', 'webhook'] as const).map((k) => ({ value: k, title: t(`agent.inbound.kind.${k}`) })))
const securityItems = computed(() => (['tls', 'starttls', 'none'] as const).map((s) => ({ value: s, title: t(`agent.inbound.security.${s}`) })))
const authItems = computed(() => (['password', 'xoauth2'] as const).map((a) => ({ value: a, title: t(`agent.inbound.auth.${a}`) })))
const actionItems = computed(() => (['move', 'flag'] as const).map((a) => ({ value: a, title: t(`agent.inbound.processed.${a}`) })))

function buildBody(): S['InboundMailboxCreate'] {
  const body: Record<string, unknown> = {}
  const put = (path: string, value: unknown) => {
    if (!locked(path)) body[path] = value
  }
  put('name', model.name.trim())
  put('kind', model.kind)
  put('team_id', model.team_id)
  put('address', model.address.trim())
  put('active', model.active)
  if (model.kind === 'imap') {
    const imap: Record<string, unknown> = {}
    const putImap = (key: string, value: unknown) => {
      if (!locked(`imap.${key}`)) imap[key] = value
    }
    putImap('host', model.host.trim())
    putImap('port', Number(model.port))
    putImap('security', model.security)
    putImap('auth', model.auth)
    putImap('username', model.username.trim())
    putImap('folder', model.folder.trim())
    putImap('processed_action', model.processed_action)
    if (model.processed_action === 'move' && model.processed_folder.trim()) putImap('processed_folder', model.processed_folder.trim())
    // Write-only: only a newly typed password is ever sent.
    if (model.password) putImap('password', model.password)
    body.imap = imap
  } else if (model.webhook_hmac_secret) {
    put('webhook_hmac_secret', model.webhook_hmac_secret)
  }
  return body as unknown as S['InboundMailboxCreate']
}

const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSave = submit(async () => {
  const body = buildBody()
  const saved = props.mailbox
    ? await unwrap(api.PATCH('/admin/inbound/mailboxes/{id}', { params: { path: { id: props.mailbox.id } }, body }))
    : await unwrap(api.POST('/admin/inbound/mailboxes', { body }))
  model.password = ''
  emit('saved', saved, !props.mailbox)
})

/** Tests the saved mailbox, but only once the form itself is valid. */
const onTest = submit(async () => {
  if (!props.mailbox) return
  testResult.value = null
  testResult.value = await unwrap(api.POST('/admin/inbound/mailboxes/{id}/test', { params: { path: { id: props.mailbox.id } } }))
})
</script>

<template>
  <v-form ref="formRef" data-testid="inbound-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="inbound-form-error">{{ formError }}</v-alert>
    <v-row density="compact">
      <v-col cols="12" sm="6">
        <v-text-field v-model="model.name" :label="$t('agent.common.name')" :rules="nameRules" :disabled="locked('name')" :error-messages="fieldErrors.name" data-testid="inbound-name" />
      </v-col>
      <v-col cols="12" sm="6">
        <v-select
          v-model="model.kind"
          :items="kindItems"
          :label="$t('agent.inbound.kindLabel')"
          :rules="[validations.required.select]"
          :disabled="!!mailbox"
          :error-messages="fieldErrors.kind"
          data-testid="inbound-kind"
        />
      </v-col>
      <v-col cols="12" sm="6">
        <v-text-field v-model="model.address" :label="$t('agent.inbound.address')" type="email" :rules="addressRules" :disabled="locked('address')" :error-messages="fieldErrors.address" data-testid="inbound-address">
          <template v-if="locked('address')" #append-inner><EnvLockBadge /></template>
        </v-text-field>
      </v-col>
      <v-col cols="12" sm="6">
        <v-select
          v-model="model.team_id"
          :items="teams"
          item-title="name"
          item-value="id"
          :label="$t('agent.common.team')"
          :hint="$t('agent.inbound.teamHint')"
          persistent-hint
          clearable
          :error-messages="fieldErrors.team_id"
        />
      </v-col>
    </v-row>

    <template v-if="model.kind === 'imap'">
      <v-divider class="my-3" />
      <div class="text-subtitle-2 mb-2">{{ $t('agent.inbound.imap') }}</div>
      <v-row density="compact">
        <v-col cols="12" sm="8">
          <v-text-field v-model="model.host" :label="$t('agent.inbound.host')" :rules="hostRules" :disabled="locked('imap.host')" :error-messages="fieldErrors['imap.host']" data-testid="inbound-host">
            <template v-if="locked('imap.host')" #append-inner><EnvLockBadge /></template>
          </v-text-field>
        </v-col>
        <v-col cols="12" sm="4">
          <v-text-field v-model="model.port" :label="$t('agent.inbound.port')" inputmode="numeric" :rules="portRules" :disabled="locked('imap.port')" :error-messages="fieldErrors['imap.port']" data-testid="inbound-port">
            <template v-if="locked('imap.port')" #append-inner><EnvLockBadge /></template>
          </v-text-field>
        </v-col>
        <v-col cols="12" sm="6">
          <v-select v-model="model.security" :items="securityItems" :label="$t('agent.inbound.securityLabel')" :rules="securityRules" :disabled="locked('imap.security')" :error-messages="fieldErrors['imap.security']" />
        </v-col>
        <v-col cols="12" sm="6">
          <v-select v-model="model.auth" :items="authItems" :label="$t('agent.inbound.authLabel')" :rules="authRules" :disabled="locked('imap.auth')" :error-messages="fieldErrors['imap.auth']" />
        </v-col>
        <v-col cols="12" sm="6">
          <v-text-field v-model="model.username" :label="$t('agent.inbound.username')" autocomplete="off" :rules="usernameRules" :disabled="locked('imap.username')" :error-messages="fieldErrors['imap.username']" data-testid="inbound-username">
            <template v-if="locked('imap.username')" #append-inner><EnvLockBadge /></template>
          </v-text-field>
        </v-col>
        <v-col cols="12" sm="6">
          <PasswordWriteOnly
            v-model="model.password"
            :label="$t('agent.inbound.password')"
            :password-set="!!mailbox?.imap?.password_set"
            :locked="locked('imap.password')"
            testid="inbound-password"
          />
        </v-col>
        <v-col cols="12" sm="4">
          <v-text-field v-model="model.folder" :label="$t('agent.inbound.folder')" :rules="folderRules" :disabled="locked('imap.folder')" :error-messages="fieldErrors['imap.folder']" />
        </v-col>
        <v-col cols="12" sm="4">
          <v-select v-model="model.processed_action" :items="actionItems" :label="$t('agent.inbound.processedLabel')" :rules="actionRules" :disabled="locked('imap.processed_action')" />
        </v-col>
        <v-col v-if="model.processed_action === 'move'" cols="12" sm="4">
          <v-text-field v-model="model.processed_folder" :label="$t('agent.inbound.processedFolder')" :disabled="locked('imap.processed_folder')" :rules="[validations.maxLength(200)]" />
        </v-col>
      </v-row>
    </template>
    <template v-else-if="model.kind === 'webhook'">
      <v-divider class="my-3" />
      <v-alert type="info" variant="tonal" density="compact" class="mb-3">{{ mailbox ? $t('agent.inbound.webhookExisting') : $t('agent.inbound.webhookHint') }}</v-alert>
      <PasswordField v-model="model.webhook_hmac_secret" :label="$t('agent.inbound.hmacSecret')" :hint="$t('agent.inbound.hmacHint')" persistent-hint autocomplete="new-password" :rules="[validations.maxLength(200)]" />
    </template>

    <v-switch v-model="model.active" :label="$t('agent.common.active')" color="primary" inset :disabled="locked('active')" class="mt-2" />

    <v-alert
      v-if="testResult"
      :type="testResult.ok ? 'success' : 'error'"
      variant="tonal"
      density="compact"
      class="mb-3"
      data-testid="inbound-test-result"
    >
      {{ testResult.ok ? $t('agent.inbound.testOk', { n: testResult.message_count ?? 0 }) : $t('agent.inbound.testFailed', { error: testResult.error || $t('agent.common.unknown') }) }}
    </v-alert>

    <div class="d-flex flex-wrap justify-end ga-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn
        v-if="mailbox && model.kind === 'imap'"
        variant="tonal"
        :loading="submitting"
        :title="$t('agent.inbound.testHint')"
        data-testid="inbound-test"
        @click="onTest()"
      >{{ $t('agent.inbound.test') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="inbound-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
