<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { mdiCheckCircleOutline, mdiAlertCircleOutline } from '@mdi/js'
import { useI18n } from 'vue-i18n'
import AuthCard from '../components/AuthCard.vue'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { useSessionStore } from '@/stores/session'
import type { Me } from '@/types'
import MfaStep from '@/components/mfa/MfaStep.vue'
import { isMfaChallenge, type MfaChallenge } from '@/components/mfa/mfa'

/** `/verify-email?token=` (FE-014): submits the token once on arrival. */
const route = useRoute()
const session = useSessionStore()
const { t } = useI18n()
const state = ref<'working' | 'done' | 'failed'>('working')
const message = ref('')
const challenge = ref<MfaChallenge | null>(null)

function onMfaDone() {
  challenge.value = null
  state.value = 'done'
}

onMounted(async () => {
  const token = typeof route.query.token === 'string' ? route.query.token : ''
  if (!token) {
    state.value = 'failed'
    message.value = t('portal.account.linkInvalid')
    return
  }
  try {
    const me = await unwrap(api.POST('/auth/verify-email', { body: { token } }))
    if (isMfaChallenge(me)) {
      // Verified; the instance policy asks for a second factor before the session (D-19 §B).
      challenge.value = me
      return
    }
    if (me && typeof me === 'object' && 'csrf_token' in me) session.set(me as unknown as Me)
    state.value = 'done'
  } catch (e) {
    state.value = 'failed'
    message.value = e instanceof ApiProblem && e.status !== 0 ? e.detail || t('portal.account.linkInvalid') : t('errors.network')
  }
})
</script>

<template>
  <AuthCard :title="$t('portal.account.verifyTitle')">
    <div class="text-center" role="status">
      <MfaStep v-if="challenge" class="text-start" :challenge="challenge" @done="onMfaDone" @restart="challenge = null; state = 'done'" />
      <v-progress-circular v-else-if="state === 'working'" indeterminate color="primary" data-testid="verify-working" />
      <div v-else-if="state === 'done'" data-testid="verify-done">
        <v-icon :icon="mdiCheckCircleOutline" size="48" color="success" class="mb-2" />
        <p class="mb-4">{{ $t('portal.account.verified') }}</p>
        <v-btn :to="session.me ? '/tickets' : '/login'" color="primary">{{ session.me ? $t('nav.myTickets') : $t('auth.signIn') }}</v-btn>
      </div>
      <div v-else data-testid="verify-failed">
        <v-icon :icon="mdiAlertCircleOutline" size="48" color="error" class="mb-2" />
        <p class="mb-4">{{ message }}</p>
        <v-btn to="/register" variant="outlined" class="ma-1">{{ $t('portal.account.registerAgain') }}</v-btn>
        <v-btn to="/login" variant="text" class="ma-1">{{ $t('auth.signIn') }}</v-btn>
      </div>
    </div>
  </AuthCard>
</template>
