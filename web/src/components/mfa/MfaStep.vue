<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Second step of a sign-in (FE-070, D-19 §B). Shown by both login pages and
 * by the magic-link, verify-email and invitation pages when the server
 * answered `MfaChallenge` instead of `Me`:
 *  - verify: TOTP, e-mailed code (send / resend after 30 s) or recovery code;
 *  - enroll (policy forces it): TOTP with QR code, or e-mail; then the
 *    recovery codes are shown once before continuing.
 * A 401 means the challenge is gone (expired, used, 5 wrong codes): start again.
 */
import { computed, defineAsyncComponent, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { useSessionStore } from '@/stores/session'
import type { Me } from '@/types'
import MfaCodeForm from './MfaCodeForm.vue'
import RecoveryCodes from './RecoveryCodes.vue'
import type { MfaChallenge, MfaMethod, MfaTotpSetup } from './mfa'

const QrCode = defineAsyncComponent(() => import('./QrCode.vue'))

const props = defineProps<{ challenge: MfaChallenge }>()
const emit = defineEmits<{ done: []; restart: [] }>()
const { t } = useI18n()
const session = useSessionStore()

const RESEND_SECONDS = 30
const enrolling = props.challenge.enrollment_required
const phase = ref<'verify' | 'enroll' | 'codes' | 'expired'>(enrolling ? 'enroll' : 'verify')
const methods = computed(() => props.challenge.methods)
const order: MfaMethod[] = ['totp', 'email', 'recovery']
const method = ref<MfaMethod | undefined>(order.find((m) => props.challenge.methods.includes(m)))
const error = ref<string | null>(null)
const emailSent = ref(false)
const sending = ref(false)
const cooldown = ref(0)
const totp = ref<MfaTotpSetup | null>(null)
const codes = ref<string[]>([])
let timer: ReturnType<typeof setInterval> | null = null

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})

function startCooldown() {
  cooldown.value = RESEND_SECONDS
  if (timer) clearInterval(timer)
  timer = setInterval(() => {
    cooldown.value = Math.max(0, cooldown.value - 1)
    if (cooldown.value === 0 && timer) {
      clearInterval(timer)
      timer = null
    }
  }, 1000)
}

/** 401 = challenge gone; anything else is shown above the form. */
function fail(e: unknown): boolean {
  if (e instanceof ApiProblem && e.status === 401) {
    phase.value = 'expired'
    return true
  }
  error.value = e instanceof ApiProblem ? (e.status === 429 ? t('mfa.step.tooMany') : e.userMessage) : t('errors.generic')
  return false
}

function pick(m: MfaMethod) {
  method.value = m
  error.value = null
}

async function sendEmail() {
  error.value = null
  sending.value = true
  try {
    await unwrap(api.POST('/auth/mfa/email/send', { body: { mfa_token: props.challenge.mfa_token } }))
    emailSent.value = true
    startCooldown()
  } catch (e) {
    fail(e)
  } finally {
    sending.value = false
  }
}

async function verify(code: string) {
  error.value = null
  try {
    const me = await unwrap(api.POST('/auth/mfa/verify', { body: { mfa_token: props.challenge.mfa_token, method: method.value!, code } }))
    session.set(me as unknown as Me)
    emit('done')
  } catch (e) {
    if (!(e instanceof ApiProblem) || e.status !== 422) {
      fail(e)
      return
    }
    throw e
  }
}

async function startTotp() {
  error.value = null
  try {
    totp.value = await unwrap(api.POST('/auth/mfa/enroll/totp', { body: { mfa_token: props.challenge.mfa_token } }))
  } catch (e) {
    fail(e)
  }
}

async function confirmEnroll(code: string) {
  error.value = null
  const m = method.value === 'email' ? 'email' : 'totp'
  try {
    const res = await unwrap(api.POST('/auth/mfa/enroll/confirm', { body: { mfa_token: props.challenge.mfa_token, method: m, code } }))
    session.set(res.me as unknown as Me)
    codes.value = res.recovery_codes
    phase.value = 'codes'
  } catch (e) {
    if (!(e instanceof ApiProblem) || e.status !== 422) {
      fail(e)
      return
    }
    throw e
  }
}

const hint = computed(() => (method.value ? t(`mfa.step.hint.${method.value}`) : ''))
</script>

<template>
  <div data-testid="mfa-step">
    <template v-if="phase === 'expired'">
      <v-alert type="warning" variant="tonal" class="mb-4" data-testid="mfa-expired">{{ $t('mfa.step.expired') }}</v-alert>
      <v-btn color="primary" block data-testid="mfa-restart" @click="emit('restart')">{{ $t('mfa.step.restart') }}</v-btn>
    </template>

    <template v-else-if="phase === 'codes'">
      <h2 class="text-h6 mb-2">{{ $t('mfa.codes.title') }}</h2>
      <RecoveryCodes :codes="codes" />
      <v-btn color="primary" block class="mt-4" data-testid="mfa-continue" @click="emit('done')">{{ $t('mfa.codes.continue') }}</v-btn>
    </template>

    <template v-else>
      <h2 class="text-h6 mb-1">{{ enrolling ? $t('mfa.step.enrollTitle') : $t('mfa.step.title') }}</h2>
      <p class="text-body-2 text-medium-emphasis mb-3">{{ enrolling ? $t('mfa.step.enrollIntro') : hint }}</p>
      <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mb-3" data-testid="mfa-error">{{ error }}</v-alert>
      <v-alert v-if="!methods.length" type="warning" variant="tonal" data-testid="mfa-no-method">{{ $t('mfa.step.noMethod') }}</v-alert>

      <div v-if="methods.length > 1" class="d-flex flex-wrap ga-2 mb-4" role="group" :aria-label="$t('mfa.step.methodLabel')">
        <v-btn
          v-for="m in methods"
          :key="m"
          :variant="method === m ? 'flat' : 'outlined'"
          :color="method === m ? 'primary' : undefined"
          size="small"
          :aria-pressed="method === m"
          :data-testid="`mfa-method-${m}`"
          @click="pick(m)"
        >
          {{ $t(`mfa.method.${m}`) }}
        </v-btn>
      </div>

      <template v-if="method === 'email'">
        <p v-if="emailSent" class="text-body-2 mb-2" role="status" data-testid="mfa-email-sent">{{ $t('mfa.step.emailSent') }}</p>
        <v-btn
          variant="tonal"
          block
          class="mb-4"
          :loading="sending"
          :disabled="cooldown > 0"
          data-testid="mfa-send-email"
          @click="sendEmail"
        >
          {{ cooldown > 0 ? $t('mfa.step.resendIn', { s: cooldown }) : emailSent ? $t('mfa.step.resend') : $t('mfa.step.sendEmail') }}
        </v-btn>
      </template>

      <template v-if="phase === 'verify' && method">
        <MfaCodeForm :key="method" :kind="method" :action="verify" />
      </template>

      <template v-else-if="phase === 'enroll' && method === 'totp'">
        <v-btn v-if="!totp" color="primary" block data-testid="mfa-enroll-totp-start" @click="startTotp">{{ $t('mfa.setup.start') }}</v-btn>
        <div v-else>
          <p class="text-body-2 mb-2">{{ $t('mfa.setup.scan') }}</p>
          <div class="d-flex justify-center mb-2"><QrCode :value="totp.otpauth_uri" :label="$t('mfa.setup.qrLabel')" /></div>
          <p class="text-body-2 mb-1">{{ $t('mfa.setup.manual') }}</p>
          <code class="d-block text-break mb-4" data-testid="mfa-secret">{{ totp.secret }}</code>
          <MfaCodeForm kind="totp" :action="confirmEnroll" :submit-label="$t('mfa.setup.confirm')" />
        </div>
      </template>
      <template v-else-if="phase === 'enroll' && method === 'email'">
        <MfaCodeForm v-if="emailSent" kind="email" :action="confirmEnroll" :submit-label="$t('mfa.setup.confirm')" />
      </template>

      <v-btn variant="text" block class="mt-2" data-testid="mfa-cancel" @click="emit('restart')">{{ $t('mfa.step.cancel') }}</v-btn>
    </template>
  </div>
</template>
