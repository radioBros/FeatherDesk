<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { mdiDevices } from '@mdi/js'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { useConfirm } from '@/composables/useConfirm'
import { useSessionStore } from '@/stores/session'
import { useUiStore } from '@/stores/ui'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import type { Session } from '../types'

/** `/profile/sessions` (FE-037): revoking the current session signs this tab out. */
const { t } = useI18n()
const router = useRouter()
const session = useSessionStore()
const ui = useUiStore()
const { confirm } = useConfirm()
const sessions = ref<Session[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

async function load() {
  loading.value = true
  try {
    sessions.value = (await unwrap(api.GET('/me/sessions'))) ?? []
  } catch (e) {
    error.value = e instanceof ApiProblem ? e.userMessage : t('errors.generic')
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function revoke(s: Session) {
  if (!(await confirm({ title: t('portal.sessions.revokeTitle'), body: s.current ? t('portal.sessions.revokeCurrent') : t('portal.sessions.revokeBody'), color: 'error' }))) return
  try {
    await unwrap(api.DELETE('/me/sessions/{id}', { params: { path: { id: s.id } } }))
  } catch (e) {
    if (!(e instanceof ApiProblem && e.status === 401)) {
      ui.notify(e instanceof ApiProblem ? e.userMessage : t('errors.generic'), 'error')
      return
    }
  }
  if (s.current) {
    session.clear()
    await router.replace('/login')
    return
  }
  sessions.value = sessions.value.filter((x) => x.id !== s.id)
}
</script>

<template>
  <v-container class="py-6" style="max-width: 860px">
    <PageHeader :title="$t('portal.sessions.title')" :subtitle="$t('portal.sessions.intro')" />
    <v-alert v-if="error" type="error" variant="tonal" class="mb-4">{{ error }}</v-alert>
    <v-progress-linear v-if="loading" indeterminate color="primary" />
    <EmptyState v-else-if="!sessions.length" :icon="mdiDevices" :title="$t('portal.sessions.empty')" body="" />
    <v-card v-else border flat>
      <v-list lines="two" data-testid="session-list">
        <v-list-item v-for="s in sessions" :key="s.id" data-testid="session-row">
          <v-list-item-title>
            {{ s.user_agent || $t('portal.sessions.unknownDevice') }}
            <v-chip v-if="s.current" size="x-small" color="primary" class="ml-2" label>{{ $t('portal.sessions.current') }}</v-chip>
          </v-list-item-title>
          <v-list-item-subtitle>
            <span v-if="s.ip">{{ s.ip }} · </span>{{ $t('portal.sessions.lastSeen') }} <RelativeTime :value="s.last_seen_at ?? s.created_at" />
          </v-list-item-subtitle>
          <template #append>
            <v-btn variant="text" color="error" data-testid="session-revoke" @click="revoke(s)">{{ $t('portal.sessions.revoke') }}</v-btn>
          </template>
        </v-list-item>
      </v-list>
    </v-card>
  </v-container>
</template>
