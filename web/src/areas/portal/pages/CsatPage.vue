<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { mdiCheckCircleOutline } from '@mdi/js'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { useSessionStore } from '@/stores/session'
import AuthCard from '../components/AuthCard.vue'
import CsatForm from '../forms/CsatForm.vue'

/**
 * `/tickets/:id/rate?score=n&token=…` (FE-036): the emailed one-click link. The
 * preselected score is submitted exactly once on arrival; a comment can follow.
 */
const route = useRoute()
const session = useSessionStore()
const { t } = useI18n()
const id = computed(() => String(route.params.id ?? ''))
const token = computed(() => {
  const v = route.query.token ?? route.query.csat_token
  return typeof v === 'string' && v ? v : null
})
const preset = computed(() => {
  const n = Number(route.query.score)
  return Number.isInteger(n) && n >= 1 && n <= 5 ? n : null
})
const state = ref<'idle' | 'sending' | 'recorded' | 'commented' | 'error'>('idle')
const error = ref('')

onMounted(async () => {
  if (preset.value === null) return
  state.value = 'sending'
  try {
    await unwrap(
      api.POST('/portal/tickets/{id}/csat', {
        params: { path: { id: id.value } },
        body: { score: preset.value, ...(token.value ? { csat_token: token.value } : {}) },
      }),
    )
    state.value = 'recorded'
  } catch (e) {
    state.value = 'error'
    error.value = e instanceof ApiProblem && e.status === 409 ? t('portal.csat.already') : e instanceof ApiProblem ? e.userMessage : t('errors.generic')
  }
})
</script>

<template>
  <AuthCard :title="$t('portal.csat.title')">
    <v-progress-linear v-if="state === 'sending'" indeterminate color="primary" />
    <div v-else-if="state === 'commented'" class="text-center" role="status" data-testid="csat-done">
      <v-icon :icon="mdiCheckCircleOutline" size="48" color="success" class="mb-2" />
      <p>{{ $t('portal.csat.thanks') }}</p>
    </div>
    <template v-else>
      <v-alert v-if="state === 'recorded'" type="success" variant="tonal" class="mb-4" data-testid="csat-recorded" role="status">
        {{ $t('portal.csat.recorded') }}
      </v-alert>
      <v-alert v-if="state === 'error'" type="warning" variant="tonal" class="mb-4" data-testid="csat-error">{{ error }}</v-alert>
      <CsatForm :ticket-id="id" :score="preset" :csat-token="token" @rated="state = 'commented'" />
    </template>
    <template #below>
      <div class="text-center mt-4">
        <v-btn :to="session.me ? `/tickets/${id}` : '/login'" variant="text">{{ session.me ? $t('portal.csat.viewTicket') : $t('auth.signIn') }}</v-btn>
      </div>
    </template>
  </AuthCard>
</template>
