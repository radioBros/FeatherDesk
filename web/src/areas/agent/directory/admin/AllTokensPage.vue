<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/admin/tokens (FE-042, admin): every API token, with revoke. */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiDelete } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import RelativeTime from '@/components/RelativeTime.vue'
import { api, unwrap } from '@/api/client'
import { useConfirm } from '@/composables/useConfirm'
import { useCursorList } from '../../shared/useCursorList'
import { useAction } from '../../shared/useAction'
import type { components } from '@/api/schema'
import ColumnCells from '@/components/ColumnCells.vue'

type S = components['schemas']
const { t } = useI18n()
const { confirm } = useConfirm()
const { run } = useAction()
const userId = ref<string | null>(null)
const list = useCursorList((cursor) =>
  unwrap(api.GET('/admin/tokens', { params: { query: { user_id: userId.value ?? undefined, cursor: cursor ?? undefined, limit: 50 } } })),
)
onMounted(() => void list.reload())

async function revoke(tok: S['ApiToken']) {
  const owner = tok.user?.name ?? t('agent.common.unknown')
  if (!(await confirm({ body: t('agent.allTokens.revokeConfirm', { name: tok.name, owner }), color: 'error', confirmText: t('agent.tokens.revoke') }))) return
  const ok = await run(async () => {
    await unwrap(api.DELETE('/admin/tokens/{id}', { params: { path: { id: tok.id } } }))
    return true
  }, t('agent.tokens.revoked'))
  if (ok) list.items.value = list.items.value.map((x) => (x.id === tok.id ? { ...x, revoked_at: new Date().toISOString() } : x))
}
function filterBy(id: string | undefined) {
  userId.value = id ?? null
  void list.reload()
}
const cols = useTableColumns('admin-tokens', () => [
  { key: 'owner', label: t('agent.allTokens.owner') },
  { key: 'name', label: t('agent.common.name'), locked: true },
  { key: 'prefix', label: t('agent.tokens.prefix'), locked: true },
  { key: 'scopes', label: t('agent.tokens.scopes'), lowPriority: true },
  { key: 'lastUsed', label: t('agent.common.lastUsed'), lowPriority: true },
  { key: 'expires', label: t('agent.common.expires'), lowPriority: true },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('agent.admin.nav.tokens')" :subtitle="$t('agent.allTokens.subtitle')" />
    <v-chip v-if="userId" closable class="mb-3" data-testid="token-user-filter" @click:close="filterBy(undefined)">{{ $t('agent.allTokens.filteredByUser') }}</v-chip>
    <v-alert v-if="list.error.value" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="list.loading.value" indeterminate />
      <EmptyState v-if="!list.loading.value && !list.items.value.length" :title="$t('agent.tokens.empty')" body="" />
      <template v-else>
      <ColumnsMenu :cols="cols" />
      <v-table density="comfortable" data-testid="all-token-table">
        <thead>
          <tr>
            <ColumnCells :cols="cols" head>
              <th data-col="owner" v-if="cols.show('owner')">{{ $t('agent.allTokens.owner') }}</th>
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
          <tr v-for="tok in list.items.value" :key="tok.id" :class="{ 'text-disabled': tok.revoked_at }" data-testid="all-token-row">
            <ColumnCells :cols="cols">
              <td data-col="owner" v-if="cols.show('owner')">
                <a v-if="tok.user" href="#" @click.prevent="filterBy(tok.user.id)">{{ tok.user.name }}</a>
                <span v-else>—</span>
              </td>
              <td data-col="name" v-if="cols.show('name')">{{ tok.name }}</td>
              <td data-col="prefix" v-if="cols.show('prefix')"><code>{{ tok.prefix }}…</code></td>
              <td data-col="scopes" v-if="cols.show('scopes')"><v-chip v-for="s in tok.scopes" :key="s" size="x-small" label class="mr-1">{{ s }}</v-chip></td>
              <td data-col="lastUsed" v-if="cols.show('lastUsed')"><RelativeTime v-if="tok.last_used_at" :value="tok.last_used_at" /><span v-else>{{ $t('agent.common.never') }}</span></td>
              <td data-col="expires" v-if="cols.show('expires')"><RelativeTime v-if="tok.expires_at" :value="tok.expires_at" /><span v-else>{{ $t('agent.common.never') }}</span></td>
              <td class="text-right">
                <v-chip v-if="tok.revoked_at" size="small" label data-testid="token-revoked">{{ $t('agent.tokens.revokedLabel') }}</v-chip>
                <v-btn v-else :icon="mdiDelete" variant="text" size="small" :aria-label="$t('agent.tokens.revoke')" data-testid="all-token-revoke" @click="revoke(tok)" />
              </td>
            </ColumnCells>
          </tr>
        </tbody>
      </v-table>
      </template>
      <div v-if="list.next.value" class="pa-2 text-center">
        <v-btn variant="text" :loading="list.loading.value" @click="list.loadMore()">{{ $t('agent.common.loadMore') }}</v-btn>
      </div>
    </v-card>
  </v-container>
</template>
