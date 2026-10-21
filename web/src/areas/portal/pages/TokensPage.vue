<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiContentCopy, mdiKeyVariant } from '@mdi/js'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { useConfirm } from '@/composables/useConfirm'
import { useUiStore } from '@/stores/ui'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import TokenForm from '../forms/TokenForm.vue'
import McpConnect from '@/components/McpConnect.vue'
import type { ApiToken, ApiTokenCreated } from '../types'

/** `/profile/tokens` (FE-037): list, create (secret shown once), revoke. */
const { t } = useI18n()
const ui = useUiStore()
const { confirm } = useConfirm()
const tokens = ref<ApiToken[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const fresh = ref<ApiTokenCreated | null>(null)

async function load() {
  loading.value = true
  error.value = null
  try {
    tokens.value = ((await unwrap(api.GET('/me/tokens'))) ?? []).filter((x) => !x.revoked_at)
  } catch (e) {
    error.value = e instanceof ApiProblem ? e.userMessage : t('errors.generic')
  } finally {
    loading.value = false
  }
}
onMounted(load)

function created(tk: ApiTokenCreated) {
  fresh.value = tk
  tokens.value.unshift(tk)
}

async function copy() {
  if (!fresh.value) return
  try {
    await navigator.clipboard.writeText(fresh.value.token)
    ui.notify(t('portal.tokens.copied'), 'success')
  } catch {
    /* clipboard blocked: the token stays visible to copy by hand */
  }
}

async function revoke(tk: ApiToken) {
  if (!(await confirm({ title: t('portal.tokens.revokeTitle'), body: t('portal.tokens.revokeBody', { name: tk.name }), color: 'error' }))) return
  try {
    await unwrap(api.DELETE('/me/tokens/{id}', { params: { path: { id: tk.id } } }))
    tokens.value = tokens.value.filter((x) => x.id !== tk.id)
    if (fresh.value?.id === tk.id) fresh.value = null
  } catch (e) {
    ui.notify(e instanceof ApiProblem ? e.userMessage : t('errors.generic'), 'error')
  }
}
</script>

<template>
  <v-container class="py-6" style="max-width: 860px">
    <PageHeader :title="$t('nav.apiTokens')" :subtitle="$t('portal.tokens.intro')" />
    <McpConnect />
    <v-alert v-if="fresh" type="success" variant="tonal" class="mb-4" data-testid="token-secret">
      <div class="mb-2">{{ $t('portal.tokens.copyNow') }}</div>
      <code class="st-secret">{{ fresh.token }}</code>
      <template #append>
        <v-btn :icon="mdiContentCopy" variant="text" :aria-label="$t('portal.tokens.copy')" @click="copy" />
      </template>
    </v-alert>
    <v-card border flat class="pa-4 mb-4">
      <h2 class="text-h6 mb-3">{{ $t('portal.tokens.new') }}</h2>
      <TokenForm @created="created" />
    </v-card>
    <v-alert v-if="error" type="error" variant="tonal" class="mb-4">{{ error }}</v-alert>
    <v-progress-linear v-if="loading" indeterminate color="primary" />
    <EmptyState v-else-if="!tokens.length" :icon="mdiKeyVariant" :title="$t('portal.tokens.empty')" body="" />
    <v-card v-else border flat>
      <v-list lines="two" data-testid="token-list">
        <v-list-item v-for="tk in tokens" :key="tk.id" :title="tk.name" data-testid="token-row">
          <v-list-item-subtitle>
            <code>{{ tk.prefix }}…</code> · {{ tk.scopes.join(', ') }} ·
            <template v-if="tk.last_used_at">{{ $t('portal.tokens.lastUsed') }} <RelativeTime :value="tk.last_used_at" /></template>
            <template v-else>{{ $t('portal.tokens.neverUsed') }}</template>
          </v-list-item-subtitle>
          <template #append>
            <v-btn variant="text" color="error" data-testid="token-revoke" @click="revoke(tk)">{{ $t('portal.tokens.revoke') }}</v-btn>
          </template>
        </v-list-item>
      </v-list>
    </v-card>
  </v-container>
</template>

<style scoped>
.st-secret { word-break: break-all; user-select: all; }
</style>
