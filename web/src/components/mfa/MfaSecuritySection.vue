<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * "Two-factor authentication" card of both profile pages (FE-072, D-19 §B).
 * Every change first confirms the password (POST /me/mfa/reauth) unless this
 * session did so within the last 10 minutes. Secrets and recovery codes are
 * shown once, never stored by the client.
 */
import { computed, defineAsyncComponent, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { useUiStore } from '@/stores/ui'
import { useConfirm } from '@/composables/useConfirm'
import MfaCodeForm from './MfaCodeForm.vue'
import MfaReauthForm from './MfaReauthForm.vue'
import RecoveryCodes from './RecoveryCodes.vue'
import type { MfaStatus, MfaTotpSetup } from './mfa'

const QrCode = defineAsyncComponent(() => import('./QrCode.vue'))

const { t } = useI18n()
const ui = useUiStore()
const { confirm } = useConfirm()
const status = ref<MfaStatus | null>(null)
const failed = ref(false)
const busy = ref(false)
const reauthOpen = ref(false)
const setup = ref<MfaTotpSetup | null>(null)
const codes = ref<string[]>([])
let pending: (() => Promise<void>) | null = null

async function load() {
  failed.value = false
  try {
    status.value = await unwrap(api.GET('/me/mfa'))
  } catch {
    failed.value = true
  }
}
onMounted(load)

const reauthValid = () => {
  const until = status.value?.reauth_expires_at
  return !!until && new Date(until).getTime() > Date.now() + 5000
}

/** Runs fn now, or after the password dialog when the re-auth window is closed. */
function withReauth(fn: () => Promise<void>) {
  if (reauthValid()) return act(fn)
  pending = fn
  reauthOpen.value = true
}
function onReauthed(s: MfaStatus) {
  status.value = s
  reauthOpen.value = false
  const fn = pending
  pending = null
  if (fn) void act(fn)
}
async function act(fn: () => Promise<void>) {
  busy.value = true
  try {
    await fn()
  } catch (e) {
    if (e instanceof ApiProblem && e.status === 403 && status.value) status.value = { ...status.value, reauth_expires_at: null }
    ui.notify(e instanceof ApiProblem ? e.userMessage : t('errors.generic'), 'error')
  } finally {
    busy.value = false
  }
}

const setupTotp = () =>
  withReauth(async () => {
    setup.value = await unwrap(api.POST('/me/mfa/totp/setup'))
  })
async function confirmTotp(code: string) {
  const res = await unwrap(api.POST('/me/mfa/totp/confirm', { body: { code } }))
  status.value = res.status
  setup.value = null
  codes.value = res.recovery_codes
  ui.notify(t('mfa.profile.totpEnabled'), 'success')
}
async function disable(method: 'totp' | 'email') {
  if (!(await confirm({ body: t(`mfa.profile.disableConfirm.${method}`), color: 'error' }))) return
  withReauth(async () => {
    status.value = await unwrap(method === 'totp' ? api.DELETE('/me/mfa/totp') : api.DELETE('/me/mfa/email'))
    ui.notify(t('mfa.profile.disabled'), 'success')
  })
}
const enableEmail = () =>
  withReauth(async () => {
    const res = await unwrap(api.POST('/me/mfa/email'))
    status.value = res.status
    codes.value = res.recovery_codes
    ui.notify(t('mfa.profile.emailEnabled'), 'success')
  })
async function regenerate() {
  if (!(await confirm({ body: t('mfa.profile.regenerateConfirm') }))) return
  withReauth(async () => {
    codes.value = (await unwrap(api.POST('/me/mfa/recovery-codes'))).recovery_codes
    await load()
  })
}
async function closeCodes() {
  codes.value = []
  await load()
}

const setupOpen = computed({
  get: () => !!setup.value,
  set: (v) => {
    if (!v) setup.value = null
  },
})
const codesOpen = computed({
  get: () => codes.value.length > 0,
  set: (v) => {
    if (!v) void closeCodes()
  },
})
</script>

<template>
  <v-card border flat :title="$t('mfa.profile.title')" class="mb-4" data-testid="mfa-section">
    <v-card-text>
      <v-alert v-if="failed" type="warning" variant="tonal" density="compact">{{ $t('mfa.profile.loadFailed') }}</v-alert>
      <v-progress-linear v-else-if="!status" indeterminate />
      <p v-else-if="!status.available" class="text-body-2 text-medium-emphasis" data-testid="mfa-unavailable">{{ $t('mfa.profile.unavailable') }}</p>
      <template v-else>
        <p class="text-body-2 mb-3">{{ $t('mfa.profile.intro') }}</p>
        <v-alert v-if="status.required && !status.enabled" type="warning" variant="tonal" density="compact" class="mb-3" data-testid="mfa-required">
          {{ $t('mfa.profile.required') }}
        </v-alert>
        <v-list density="compact" class="pa-0" role="none">
          <v-list-item :title="$t('mfa.method.totp')" :subtitle="status.totp_enabled ? $t('mfa.profile.on') : $t('mfa.profile.off')" role="none" data-testid="mfa-row-totp">
            <template #append>
              <v-btn size="small" variant="tonal" class="mr-1" :loading="busy" data-testid="mfa-totp-setup" @click="setupTotp">
                {{ status.totp_enabled ? $t('mfa.profile.replace') : $t('mfa.profile.setUp') }}
              </v-btn>
              <v-btn v-if="status.totp_enabled" size="small" variant="text" color="error" data-testid="mfa-totp-disable" @click="disable('totp')">
                {{ $t('mfa.profile.turnOff') }}
              </v-btn>
            </template>
          </v-list-item>
          <v-list-item
            :title="$t('mfa.method.email')"
            :subtitle="status.email_enabled ? $t('mfa.profile.on') : status.email_available ? $t('mfa.profile.off') : $t('mfa.profile.emailUnavailable')"
            role="none"
            data-testid="mfa-row-email"
          >
            <template #append>
              <v-btn v-if="status.email_enabled" size="small" variant="text" color="error" data-testid="mfa-email-disable" @click="disable('email')">
                {{ $t('mfa.profile.turnOff') }}
              </v-btn>
              <v-btn v-else size="small" variant="tonal" :disabled="!status.email_available" :loading="busy" data-testid="mfa-email-enable" @click="enableEmail">
                {{ $t('mfa.profile.turnOn') }}
              </v-btn>
            </template>
          </v-list-item>
          <v-list-item
            v-if="status.enabled"
            :title="$t('mfa.method.recovery')"
            :subtitle="$t('mfa.profile.remaining', { n: status.recovery_codes_remaining })"
            role="none"
            data-testid="mfa-row-recovery"
          >
            <template #append>
              <v-btn size="small" variant="tonal" data-testid="mfa-recovery-regenerate" @click="regenerate">{{ $t('mfa.profile.regenerate') }}</v-btn>
            </template>
          </v-list-item>
        </v-list>
      </template>
    </v-card-text>

    <v-dialog v-model="reauthOpen" max-width="440">
      <v-card :title="$t('mfa.reauth.title')">
        <v-card-text><MfaReauthForm v-if="reauthOpen" @done="onReauthed" @cancel="reauthOpen = false" /></v-card-text>
      </v-card>
    </v-dialog>

    <v-dialog v-model="setupOpen" max-width="480">
      <v-card v-if="setup" :title="$t('mfa.setup.title')" data-testid="mfa-setup-dialog">
        <v-card-text>
          <p class="text-body-2 mb-2">{{ $t('mfa.setup.scan') }}</p>
          <div class="d-flex justify-center mb-2"><QrCode :value="setup.otpauth_uri" :label="$t('mfa.setup.qrLabel')" /></div>
          <p class="text-body-2 mb-1">{{ $t('mfa.setup.manual') }}</p>
          <code class="d-block text-break mb-4" data-testid="mfa-secret">{{ setup.secret }}</code>
          <MfaCodeForm kind="totp" :action="confirmTotp" :submit-label="$t('mfa.setup.confirm')" />
        </v-card-text>
        <v-card-actions><v-spacer /><v-btn variant="text" @click="setup = null">{{ $t('app.cancel') }}</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="codesOpen" max-width="480" persistent>
      <v-card :title="$t('mfa.codes.title')" data-testid="mfa-codes-dialog">
        <v-card-text><RecoveryCodes :codes="codes" /></v-card-text>
        <v-card-actions><v-spacer /><v-btn color="primary" data-testid="mfa-codes-done" @click="closeCodes">{{ $t('mfa.codes.saved') }}</v-btn></v-card-actions>
      </v-card>
    </v-dialog>
  </v-card>
</template>
