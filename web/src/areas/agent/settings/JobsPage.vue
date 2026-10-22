<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/admin/jobs (FE-042, admin): deferred jobs that are retrying or dead,
 * with an error/payload viewer and Retry (after confirm).
 */
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { mdiReplay } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import { api, unwrap } from '@/api/client'
import { absoluteTime } from '@/utils/time'
import { useConfirm } from '@/composables/useConfirm'
import { useCursorList } from '../shared/useCursorList'
import { useAction } from '../shared/useAction'
import type { components } from '@/api/schema'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import DataTable from '@/components/DataTable.vue'

type Job = components['schemas']['DeferredJob'] & { payload?: unknown }
type Status = components['schemas']['DeferredJobStatus']
const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const { confirm } = useConfirm()
const { run, busy } = useAction()
const status = ref<Status>(route.query.status === 'retrying' ? 'retrying' : 'dead')
const open = ref<string | null>(null)

const list = useCursorList<Job>((cursor) =>
  unwrap(api.GET('/admin/jobs', { params: { query: { status: status.value, limit: 50, ...(cursor ? { cursor } : {}) } } })),
)
onMounted(list.reload)
watch(status, (s) => {
  void router.replace({ query: { ...route.query, status: s } })
  void list.reload()
})

async function retry(job: Job) {
  const ok = await confirm({ title: t('agent.jobs.retryTitle'), body: t('agent.jobs.retryBody', { kind: job.kind }), confirmText: t('agent.jobs.retry') })
  if (!ok) return
  const res = await run(() => unwrap(api.POST('/admin/jobs/{id}/retry', { params: { path: { id: job.id } } })), t('agent.jobs.retried'))
  if (res) {
    const i = list.items.value.findIndex((j) => j.id === job.id)
    if (i >= 0) list.items.value[i] = { ...list.items.value[i]!, ...res }
  }
}
const when = (s?: string | null) => (s ? absoluteTime(new Date(s), locale.value) : '—')

const cols = useTableColumns('jobs', () => [
  { key: 'kind', label: t('agent.jobs.kind'), locked: true },
  { key: 'status', label: t('agent.common.status') },
  { key: 'attempts', label: t('agent.jobs.attempts'), lowPriority: true },
  { key: 'createdAt', label: t('agent.common.createdAt'), lowPriority: true },
  { key: 'nextRun', label: t('agent.jobs.nextRun'), lowPriority: true },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('agent.admin.nav.jobs')" :subtitle="$t('agent.jobs.subtitle')" />
    <v-btn-toggle v-model="status" mandatory density="comfortable" variant="outlined" color="primary" class="mb-4" data-testid="jobs-status">
      <v-btn value="dead" data-testid="jobs-dead">{{ $t('agent.jobs.dead') }}</v-btn>
      <v-btn value="retrying" data-testid="jobs-retrying">{{ $t('agent.jobs.retrying') }}</v-btn>
    </v-btn-toggle>
    <v-alert v-if="list.error.value" type="error" variant="tonal" class="mb-4" data-testid="load-error">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="list.loading.value" indeterminate />
      <EmptyState v-if="!list.loading.value && !list.items.value.length" :title="$t('agent.jobs.empty')" body="" />
      <template v-else>
        <ColumnsMenu :cols="cols" />
        <!-- server-sort without a sortable column: the API has no sort, rows keep its order. -->
        <DataTable
          :cols="cols"
          :items="list.items.value"
          :loading="list.loading.value"
          server-sort
          :has-more="!!list.next.value"
          actions
          :expanded="open ? [open] : []"
          density="comfortable"
          :row-props="{ 'data-testid': 'job-row' }"
          data-testid="jobs-table"
          @update:expanded="(v: string[]) => (open = v.at(-1) ?? null)"
          @load-more="list.loadMore()"
        >
          <template #[`item.kind`]="{ item }"><code>{{ item.kind }}</code></template>
          <template #[`item.status`]="{ item }">
            <v-chip :color="item.status === 'dead' ? 'error' : 'warning'" size="small" label data-testid="job-status">
              {{ $t(`agent.jobs.${item.status}`) }}
            </v-chip>
          </template>
          <template #[`item.attempts`]="{ item }">{{ item.attempts }}</template>
          <template #[`item.createdAt`]="{ item }"><span class="text-no-wrap">{{ when(item.created_at) }}</span></template>
          <template #[`item.nextRun`]="{ item }"><span class="text-no-wrap">{{ when(item.next_run_at) }}</span></template>
          <template #[`item.actions`]="{ item }">
            <div class="text-no-wrap">
              <v-btn variant="text" size="small" data-testid="job-details" @click="open = open === item.id ? null : item.id">{{ $t('agent.common.details') }}</v-btn>
              <v-btn variant="tonal" size="small" color="primary" :prepend-icon="mdiReplay" :disabled="busy" data-testid="job-retry" @click="retry(item)">
                {{ $t('agent.jobs.retry') }}
              </v-btn>
            </div>
          </template>
          <template #expanded-row="{ item, columns }">
            <tr>
              <td :colspan="columns.length">
                <div class="text-caption text-medium-emphasis mt-2">{{ $t('agent.jobs.lastError') }}</div>
                <pre class="fd-pre" data-testid="job-error">{{ item.last_error || '—' }}</pre>
                <template v-if="item.payload !== undefined">
                  <div class="text-caption text-medium-emphasis">{{ $t('agent.jobs.payload') }}</div>
                  <pre class="fd-pre" data-testid="job-payload">{{ JSON.stringify(item.payload, null, 2) }}</pre>
                </template>
              </td>
            </tr>
          </template>
        </DataTable>
      </template>
    </v-card>
  </v-container>
</template>

<style scoped>
.fd-pre { white-space: pre-wrap; word-break: break-all; font-size: 12px; margin: 4px 0 8px; }
</style>
