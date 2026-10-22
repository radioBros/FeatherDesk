<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AuthCard from '../components/AuthCard.vue'
import { apiFetch } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { useSessionStore } from '@/stores/session'
import type { Me } from '@/types'
import MfaStep from '@/components/mfa/MfaStep.vue'
import { isMfaChallenge, type MfaChallenge } from '@/components/mfa/mfa'

/** `/magic-link?token=` (FE-034): consumes the one-time token and signs in. */
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const { t } = useI18n()
const error = ref<string | null>(null)
const challenge = ref<MfaChallenge | null>(null)

onMounted(async () => {
  const token = typeof route.query.token === 'string' ? route.query.token : ''
  if (!token) {
    error.value = t('portal.account.linkInvalid')
    return
  }
  try {
    const me = await apiFetch<Me | MfaChallenge>('/auth/magic-link', { method: 'POST', body: JSON.stringify({ token }) })
    if (isMfaChallenge(me)) {
      // The link proved the mailbox; a second factor is still required (D-19 §B).
      challenge.value = me
      return
    }
    if (me && 'csrf_token' in me) session.set(me)
    else await session.refresh()
    await router.replace('/tickets')
  } catch (e) {
    error.value = e instanceof ApiProblem && e.status !== 0 ? e.detail || t('portal.account.linkInvalid') : t('errors.network')
  }
})
</script>

<template>
  <AuthCard :title="$t('portal.account.magicTitle')">
    <div class="text-center" role="status">
      <MfaStep v-if="challenge" class="text-start" :challenge="challenge" @done="router.replace('/tickets')" @restart="router.replace('/login')" />
      <div v-else-if="error" data-testid="magic-failed">
        <v-alert type="warning" variant="tonal" class="mb-4">{{ error }}</v-alert>
        <v-btn to="/login" color="primary">{{ $t('portal.account.backToLogin') }}</v-btn>
      </div>
      <v-progress-circular v-else indeterminate color="primary" data-testid="magic-working" />
    </div>
  </AuthCard>
</template>
