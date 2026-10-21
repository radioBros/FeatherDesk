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

type Entry = components['schemas']['AuditEntry']
const { locale } = useI18n()
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
      <v-table v-else density="compact" data-testid="audit-table">
        <thead>
          <tr>
            <th>{{ $t('agent.audit.when') }}</th>
            <th>{{ $t('agent.audit.actor') }}</th>
            <th>{{ $t('agent.audit.action') }}</th>
            <th>{{ $t('agent.audit.target') }}</th>
            <th>{{ $t('agent.audit.ip') }}</th>
            <th />
          </tr>
        </thead>
        <tbody>
          <template v-for="e in list.items.value" :key="e.id">
            <tr data-testid="audit-row">
              <td class="text-no-wrap">{{ when(e.created_at) }}</td>
              <td>{{ e.actor?.name ?? $t('agent.audit.system') }}</td>
              <td><code>{{ e.action }}</code></td>
              <td class="text-truncate" style="max-width: 280px">{{ target(e) }}</td>
              <td>{{ e.ip ?? '—' }}</td>
              <td class="text-right">
                <v-btn v-if="e.data && Object.keys(e.data).length" variant="text" size="small" @click="open = open === e.id ? null : e.id">
                  {{ $t('agent.common.details') }}
                </v-btn>
              </td>
            </tr>
            <tr v-if="open === e.id">
              <td colspan="6"><pre class="st-pre" data-testid="audit-data">{{ JSON.stringify(e.data, null, 2) }}</pre></td>
            </tr>
          </template>
        </tbody>
      </v-table>
      <div v-if="list.next.value" class="pa-3 text-center">
        <v-btn variant="tonal" :loading="list.loading.value" data-testid="load-more" @click="list.loadMore">{{ $t('agent.common.loadMore') }}</v-btn>
      </div>
    </v-card>
  </v-container>
</template>

<style scoped>
.st-pre { white-space: pre-wrap; word-break: break-all; font-size: 12px; margin: 8px 0; }
</style>
