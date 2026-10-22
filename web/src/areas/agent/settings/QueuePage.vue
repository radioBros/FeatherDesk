<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/admin/queue (FE-029, admin): in-memory queue depths, workers,
 * dispatcher lag and deferred-job counts. Live through the `admin:system`
 * topic (any event or resync refetches); a manual refresh otherwise. No timers.
 */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiAlertOctagonOutline, mdiRefresh, mdiTimerSandEmpty, mdiTimerSyncOutline } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import StatCard from '@/components/StatCard.vue'
import { api, unwrap } from '@/api/client'
import { useRealtime } from '@/composables/useRealtime'
import type { components } from '@/api/schema'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import DataTable from '@/components/DataTable.vue'

const { t } = useI18n()
type QueueRow = components['schemas']['QueueStatus']['queues'][number]
const q = ref<components['schemas']['QueueStatus'] | null>(null)
const loading = ref(true)
const failed = ref(false)
let pending: Promise<void> | null = null

function load(): Promise<void> {
  // Coalesce bursts of realtime events into one request at a time.
  pending ??= (async () => {
    loading.value = true
    failed.value = false
    try {
      q.value = await unwrap(api.GET('/admin/queue'))
    } catch {
      failed.value = true
    } finally {
      loading.value = false
      pending = null
    }
  })()
  return pending
}
onMounted(load)
useRealtime(['admin:system'], { onEvent: () => void load(), onResync: () => void load() })

const pct = (depth: number, cap: number) => (cap > 0 ? Math.min(100, Math.round((depth / cap) * 100)) : 0)

const cols = useTableColumns('queue', () => [
  { key: 'name', label: t('agent.queue.name'), locked: true },
  { key: 'depth', label: t('agent.queue.depth') },
  { key: 'capacity', label: t('agent.queue.capacity'), lowPriority: true },
  { key: 'workers', label: t('agent.queue.workers'), lowPriority: true },
  { key: 'fill', label: t('agent.queue.fill'), width: 280, sortValue: (r: QueueRow) => pct(r.depth, r.capacity) },
])
</script>

<template>
  <v-container fluid class="pa-4" style="max-width: 1100px">
    <PageHeader :title="$t('agent.admin.nav.queue')" :subtitle="$t('agent.queue.subtitle')">
      <template #actions>
        <v-btn :prepend-icon="mdiRefresh" variant="tonal" :loading="loading" data-testid="queue-refresh" @click="load">{{ $t('agent.common.refresh') }}</v-btn>
      </template>
    </PageHeader>
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4" data-testid="load-error">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-progress-linear v-if="loading && !q" indeterminate />
    <template v-if="q">
      <v-row class="mb-2">
        <v-col cols="12" sm="4">
          <StatCard section="automation" :icon="mdiTimerSyncOutline" :label="$t('agent.queue.retrying')" data-testid="queue-retrying">
            <div class="text-h4">{{ q.deferred.retrying }}</div>
          </StatCard>
        </v-col>
        <v-col cols="12" sm="4">
          <StatCard :section="q.deferred.dead ? 'danger' : 'system'" :icon="mdiAlertOctagonOutline" :label="$t('agent.queue.dead')" :to="'/agent/admin/jobs?status=dead'" data-testid="queue-dead">
            <div class="text-h4" :class="q.deferred.dead ? 'text-error' : ''">{{ q.deferred.dead }}</div>
            <template #footer><div class="text-caption text-high-emphasis mt-1">{{ $t('agent.queue.viewDead') }}</div></template>
          </StatCard>
        </v-col>
        <v-col cols="12" sm="4">
          <StatCard section="queue" :icon="mdiTimerSandEmpty" :label="$t('agent.queue.lag')" data-testid="queue-lag">
            <div class="text-h4">{{ q.dispatcher?.lag_events ?? 0 }}</div>
          </StatCard>
        </v-col>
      </v-row>
      <v-card border flat>
        <EmptyState v-if="!q.queues.length" :title="$t('agent.queue.empty')" body="" />
        <!-- A labelled region (FE-032). On a narrow screen the table scrolls sideways inside DataTable's own wrapper, which is not focusable. -->
        <div v-else class="fd-scroll" tabindex="0" role="region" :aria-label="$t('agent.admin.nav.queue')">
          <ColumnsMenu :cols="cols" />
          <DataTable :cols="cols" :items="q.queues" item-value="name" density="comfortable" :row-props="{ 'data-testid': 'queue-row' }" data-testid="queue-table">
            <template #[`item.name`]="{ item }"><span class="font-weight-medium">{{ item.name }}</span></template>
            <template #[`item.depth`]="{ item }">{{ item.depth }}</template>
            <template #[`item.capacity`]="{ item }">{{ item.capacity }}</template>
            <template #[`item.workers`]="{ item }">{{ item.workers ?? '—' }}</template>
            <template #[`item.fill`]="{ item }">
              <v-progress-linear
                :model-value="pct(item.depth, item.capacity)"
                :color="pct(item.depth, item.capacity) >= 80 ? 'error' : 'primary'"
                height="8"
                rounded
                :aria-label="$t('agent.queue.fill')"
              />
            </template>
          </DataTable>
        </div>
      </v-card>
    </template>
  </v-container>
</template>

<style scoped>
.fd-scroll { overflow-x: auto; }
</style>
