<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/profile/tokens (FE-030): my API tokens. A new token is shown exactly
 * once from a local ref that is cleared when its dialog closes, so the string
 * leaves the DOM and is never put in a store.
 */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiDelete, mdiPlus } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import RelativeTime from '@/components/RelativeTime.vue'
import TokenForm from './TokenForm.vue'
import SecretOnce from '../shared/SecretOnce.vue'
import McpConnect from '@/components/McpConnect.vue'
import { api, unwrap } from '@/api/client'
import { useConfirm } from '@/composables/useConfirm'
import { useSessionStore } from '@/stores/session'
import { useAction } from '../shared/useAction'
import type { components } from '@/api/schema'
import ColumnCells from '@/components/ColumnCells.vue'

type S = components['schemas']
const { t } = useI18n()
const session = useSessionStore()
const { confirm } = useConfirm()
const { run } = useAction()
const tokens = ref<S['ApiToken'][]>([])
const loading = ref(true)
const failed = ref(false)
const formOpen = ref(false)
/** The plaintext token, only between creation and closing its dialog. */
const created = ref<string | null>(null)

async function load() {
  loading.value = true
  failed.value = false
  try {
    tokens.value = (await unwrap(api.GET('/me/tokens'))) ?? []
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(() => void load())

function onCreated(tok: S['ApiTokenCreated']) {
  formOpen.value = false
  created.value = tok.token
  const { token: _secret, ...rest } = tok
  tokens.value = [rest, ...tokens.value]
}
function closeSecret() {
  created.value = null
}
async function revoke(tok: S['ApiToken']) {
  if (!(await confirm({ body: t('agent.tokens.revokeConfirm', { name: tok.name }), color: 'error', confirmText: t('agent.tokens.revoke') }))) return
  const ok = await run(async () => {
    await unwrap(api.DELETE('/me/tokens/{id}', { params: { path: { id: tok.id } } }))
    return true
  }, t('agent.tokens.revoked'))
  if (ok) tokens.value = tokens.value.filter((x) => x.id !== tok.id)
}
const cols = useTableColumns('agent-tokens', () => [
  { key: 'name', label: t('agent.common.name'), locked: true },
  { key: 'prefix', label: t('agent.tokens.prefix'), locked: true },
  { key: 'scopes', label: t('agent.tokens.scopes'), lowPriority: true },
  { key: 'lastUsed', label: t('agent.common.lastUsed'), lowPriority: true },
  { key: 'expires', label: t('agent.common.expires'), lowPriority: true },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('nav.apiTokens')" :subtitle="$t('agent.tokens.subtitle')">
      <template #actions>
        <v-btn color="primary" :prepend-icon="mdiPlus" data-testid="token-new" @click="formOpen = true">{{ $t('agent.tokens.new') }}</v-btn>
      </template>
    </PageHeader>
    <McpConnect />
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="loading" indeterminate />
      <EmptyState v-if="!loading && !tokens.length" :title="$t('agent.tokens.empty')" body="" />
      <template v-else>
      <ColumnsMenu :cols="cols" />
      <v-table density="comfortable" data-testid="token-table">
        <thead>
          <tr>
            <ColumnCells :cols="cols" head>
              <th data-col="name" v-if="cols.show('name')">{{ $t('agent.common.name') }}</th>
              <th data-col="prefix" v-if="cols.show('prefix')">{{ $t('agent.tokens.prefix') }}</th>
              <th data-col="scopes" v-if="cols.show('scopes')">{{ $t('agent.tokens.scopes') }}</th>
              <th data-col="lastUsed" v-if="cols.show('lastUsed')">{{ $t('agent.common.lastUsed') }}</th>
              <th data-col="expires" v-if="cols.show('expires')">{{ $t('agent.common.expires') }}</th>
              <th class="text-right">{{ $t('agent.common.actions') }}</th>
            </ColumnCells>
          </tr>
        </thead>
        <tbody>
          <tr v-for="tok in tokens" :key="tok.id" data-testid="token-row">
            <ColumnCells :cols="cols">
              <td data-col="name" v-if="cols.show('name')">{{ tok.name }}</td>
              <td data-col="prefix" v-if="cols.show('prefix')"><code>{{ tok.prefix }}…</code></td>
              <td data-col="scopes" v-if="cols.show('scopes')"><v-chip v-for="s in tok.scopes" :key="s" size="x-small" label class="mr-1">{{ s }}</v-chip></td>
              <td data-col="lastUsed" v-if="cols.show('lastUsed')"><RelativeTime v-if="tok.last_used_at" :value="tok.last_used_at" /><span v-else>{{ $t('agent.common.never') }}</span></td>
              <td data-col="expires" v-if="cols.show('expires')"><RelativeTime v-if="tok.expires_at" :value="tok.expires_at" /><span v-else>{{ $t('agent.common.never') }}</span></td>
              <td class="text-right">
                <v-btn :icon="mdiDelete" variant="text" size="small" :aria-label="$t('agent.tokens.revoke')" data-testid="token-revoke" @click="revoke(tok)" />
              </td>
            </ColumnCells>
          </tr>
        </tbody>
      </v-table>
      </template>
    </v-card>

    <v-dialog v-model="formOpen" max-width="560">
      <v-card :title="$t('agent.tokens.new')">
        <v-card-text>
          <TokenForm v-if="formOpen" :allow-admin="session.isAdmin" @created="onCreated" @cancel="formOpen = false" />
        </v-card-text>
      </v-card>
    </v-dialog>
    <v-dialog :model-value="created !== null" max-width="560" persistent>
      <v-card v-if="created !== null" :title="$t('agent.tokens.createdTitle')" data-testid="token-created-dialog">
        <v-card-text>
          <SecretOnce :value="created" :label="$t('agent.tokens.token')" />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn color="primary" variant="flat" data-testid="token-created-close" @click="closeSecret">{{ $t('agent.common.done') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </v-container>
</template>
