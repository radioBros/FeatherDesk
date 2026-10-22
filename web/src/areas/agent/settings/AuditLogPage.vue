<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/admin/audit (FE-042, admin): append-only audit log with filters and cursor paging. */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import AuditFilterForm from './AuditFilterForm.vue'
import type { AuditFilter } from './types'
import { api, unwrap } from '@/api/client'
import { absoluteTime } from '@/utils/time'
import { useCursorList } from '../shared/useCursorList'
import type { components } from '@/api/schema'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'

type Entry = components['schemas']['AuditEntry']
const { t, locale } = useI18n()
const filter = ref<AuditFilter>({ action: '', actor_id: null, since: '' })
const open = ref<string | null>(null)

const list = useCursorList<Entry>((cursor) => {
  const f = filter.value
  const query: Record<string, string | number> = { limit: 50 }
  if (cursor) query.cursor = cursor
  if (f.action) query.action = f.action
  if (f.actor_id) query.actor_id = f.actor_id
  // A date filter means "from the start of that day" (UTC).
  if (f.since) query.since = `${f.since}T00:00:00Z`
  return unwrap(api.GET('/admin/audit-log', { params: { query } }))
})
onMounted(list.reload)

function apply(f: AuditFilter) {
  filter.value = f
  void list.reload()
}
const when = (s: string) => absoluteTime(new Date(s), locale.value)
const target = (e: Entry) => (e.target_id ? `${e.target_type} · ${e.target_id}` : e.target_type)

const cols = useTableColumns('audit-log', () => [
  { key: 'when', label: t('agent.audit.when') },
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
        <v-table density="compact" data-testid="audit-table">
          <thead>
            <tr>
              <th v-if="cols.show('when')">{{ $t('agent.audit.when') }}</th>
              <th v-if="cols.show('actor')">{{ $t('agent.audit.actor') }}</th>
              <th v-if="cols.show('action')">{{ $t('agent.audit.action') }}</th>
              <th v-if="cols.show('target')">{{ $t('agent.audit.target') }}</th>
              <th v-if="cols.show('ip')">{{ $t('agent.audit.ip') }}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            <template v-for="e in list.items.value" :key="e.id">
              <tr data-testid="audit-row">
                <td class="text-no-wrap" v-if="cols.show('when')">{{ when(e.created_at) }}</td>
                <td v-if="cols.show('actor')">{{ e.actor?.name ?? $t('agent.audit.system') }}</td>
                <td v-if="cols.show('action')"><code>{{ e.action }}</code></td>
                <td class="text-truncate" style="max-width: 280px" v-if="cols.show('target')">{{ target(e) }}</td>
                <td v-if="cols.show('ip')">{{ e.ip ?? '—' }}</td>
                <td class="text-right">
                  <v-btn v-if="e.data && Object.keys(e.data).length" variant="text" size="small" @click="open = open === e.id ? null : e.id">
                    {{ $t('agent.common.details') }}
                  </v-btn>
                </td>
              </tr>
              <tr v-if="open === e.id">
                <td :colspan="cols.count.value + 1"><pre class="fd-pre" data-testid="audit-data">{{ JSON.stringify(e.data, null, 2) }}</pre></td>
              </tr>
            </template>
          </tbody>
        </v-table>
      </template>
      <div v-if="list.next.value" class="pa-3 text-center">
        <v-btn variant="tonal" :loading="list.loading.value" data-testid="load-more" @click="list.loadMore">{{ $t('agent.common.loadMore') }}</v-btn>
      </div>
    </v-card>
  </v-container>
</template>

<style scoped>
.fd-pre { white-space: pre-wrap; word-break: break-all; font-size: 12px; margin: 8px 0; }
</style>
