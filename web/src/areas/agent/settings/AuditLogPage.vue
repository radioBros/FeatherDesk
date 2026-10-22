<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/admin/audit (FE-042, admin): append-only audit log with filters and cursor paging. */
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import AuditFilterForm from './AuditFilterForm.vue'
import type { AuditFilter } from './types'
import { api, unwrap } from '@/api/client'
import { absoluteTime } from '@/utils/time'
import { useCursorList } from '../shared/useCursorList'
import type { components, operations } from '@/api/schema'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import DataTable from '@/components/DataTable.vue'

type Entry = components['schemas']['AuditEntry']
const { t, locale } = useI18n()
const filter = ref<AuditFilter>({ action: '', actor_id: null, since: '' })
const open = ref<string | null>(null)
type AuditQuery = NonNullable<operations['listAuditLog']['parameters']['query']>
const sort = ref<NonNullable<AuditQuery['sort']> | null>(null)

const list = useCursorList<Entry>((cursor) => {
  const f = filter.value
  const query: Record<string, string | number> = { limit: 50 }
  if (cursor) query.cursor = cursor
  if (sort.value) query.sort = sort.value
  if (f.action) query.action = f.action
  if (f.actor_id) query.actor_id = f.actor_id
  // A date filter means "from the start of that day" (UTC).
  if (f.since) query.since = `${f.since}T00:00:00Z`
  return unwrap(api.GET('/admin/audit-log', { params: { query } }))
})
onMounted(list.reload)
watch(sort, () => void list.reload())

function apply(f: AuditFilter) {
  filter.value = f
  void list.reload()
}
const when = (s: string) => absoluteTime(new Date(s), locale.value)
const target = (e: Entry) => (e.target_id ? `${e.target_type} · ${e.target_id}` : e.target_type)

const cols = useTableColumns('audit-log', () => [
  { key: 'when', label: t('agent.audit.when'), sort: { asc: 'created_at', desc: '-created_at' } },
  { key: 'actor', label: t('agent.audit.actor'), lowPriority: true },
  { key: 'action', label: t('agent.audit.action'), locked: true },
  { key: 'target', label: t('agent.audit.target'), lowPriority: true },
  { key: 'ip', label: t('agent.audit.ip'), lowPriority: true },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('agent.admin.nav.audit')" :subtitle="$t('agent.audit.subtitle')" />
    <v-card border flat class="pa-3 mb-4">
      <AuditFilterForm :filter="filter" @apply="apply" />
    </v-card>
    <v-alert v-if="list.error.value" type="error" variant="tonal" class="mb-4" data-testid="load-error">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="list.loading.value" indeterminate />
      <EmptyState v-if="!list.loading.value && !list.items.value.length" :title="$t('agent.audit.empty')" body="" />
      <template v-else>
        <ColumnsMenu :cols="cols" />
        <DataTable
          :cols="cols"
          :items="list.items.value"
          :loading="list.loading.value"
          server-sort
          :sort="sort"
          :has-more="!!list.next.value"
          actions
          :expanded="open ? [open] : []"
          density="compact"
          :row-props="{ 'data-testid': 'audit-row' }"
          data-testid="audit-table"
          @update:sort="(v: string | null) => (sort = v as typeof sort)"
          @update:expanded="(v: string[]) => (open = v.at(-1) ?? null)"
          @load-more="list.loadMore()"
        >
          <template #[`item.when`]="{ item }"><span class="text-no-wrap">{{ when(item.created_at) }}</span></template>
          <template #[`item.actor`]="{ item }">{{ item.actor?.name ?? $t('agent.audit.system') }}</template>
          <template #[`item.action`]="{ item }"><code>{{ item.action }}</code></template>
          <template #[`item.target`]="{ item }"><div class="text-truncate" style="max-width: 280px">{{ target(item) }}</div></template>
          <template #[`item.ip`]="{ item }">{{ item.ip ?? '—' }}</template>
          <template #[`item.actions`]="{ item }">
            <v-btn v-if="item.data && Object.keys(item.data).length" variant="text" size="small" @click="open = open === item.id ? null : item.id">
              {{ $t('agent.common.details') }}
            </v-btn>
          </template>
          <template #expanded-row="{ item, columns }">
            <tr>
              <td :colspan="columns.length"><pre class="fd-pre" data-testid="audit-data">{{ JSON.stringify(item.data, null, 2) }}</pre></td>
            </tr>
          </template>
        </DataTable>
      </template>
    </v-card>
  </v-container>
</template>

<style scoped>
.fd-pre { white-space: pre-wrap; word-break: break-all; font-size: 12px; margin: 8px 0; }
</style>
