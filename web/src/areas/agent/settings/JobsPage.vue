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
        <v-table density="comfortable" data-testid="jobs-table">
          <thead>
            <tr>
              <th v-if="cols.show('kind')">{{ $t('agent.jobs.kind') }}</th>
              <th v-if="cols.show('status')">{{ $t('agent.common.status') }}</th>
              <th v-if="cols.show('attempts')">{{ $t('agent.jobs.attempts') }}</th>
              <th v-if="cols.show('createdAt')">{{ $t('agent.common.createdAt') }}</th>
              <th v-if="cols.show('nextRun')">{{ $t('agent.jobs.nextRun') }}</th>
              <th class="text-right">{{ $t('agent.common.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <template v-for="j in list.items.value" :key="j.id">
              <tr data-testid="job-row">
                <td v-if="cols.show('kind')"><code>{{ j.kind }}</code></td>
                <td v-if="cols.show('status')">
                  <v-chip :color="j.status === 'dead' ? 'error' : 'warning'" size="small" label data-testid="job-status">
                    {{ $t(`agent.jobs.${j.status}`) }}
                  </v-chip>
                </td>
                <td v-if="cols.show('attempts')">{{ j.attempts }}</td>
                <td class="text-no-wrap" v-if="cols.show('createdAt')">{{ when(j.created_at) }}</td>
                <td class="text-no-wrap" v-if="cols.show('nextRun')">{{ when(j.next_run_at) }}</td>
                <td class="text-right text-no-wrap">
                  <v-btn variant="text" size="small" data-testid="job-details" @click="open = open === j.id ? null : j.id">{{ $t('agent.common.details') }}</v-btn>
                  <v-btn variant="tonal" size="small" color="primary" :prepend-icon="mdiReplay" :disabled="busy" data-testid="job-retry" @click="retry(j)">
                    {{ $t('agent.jobs.retry') }}
                  </v-btn>
                </td>
              </tr>
              <tr v-if="open === j.id">
                <td :colspan="cols.count.value + 1">
                  <div class="text-caption text-medium-emphasis mt-2">{{ $t('agent.jobs.lastError') }}</div>
                  <pre class="st-pre" data-testid="job-error">{{ j.last_error || '—' }}</pre>
                  <template v-if="j.payload !== undefined">
                    <div class="text-caption text-medium-emphasis">{{ $t('agent.jobs.payload') }}</div>
                    <pre class="st-pre" data-testid="job-payload">{{ JSON.stringify(j.payload, null, 2) }}</pre>
                  </template>
                </td>
              </tr>
            </template>
          </tbody>
        </v-table>
      </template>
      <div v-if="list.next.value" class="pa-3 text-center">
        <v-btn variant="tonal" :loading="list.loading.value" @click="list.loadMore">{{ $t('agent.common.loadMore') }}</v-btn>
      </div>
    </v-card>
  </v-container>
</template>

<style scoped>
.st-pre { white-space: pre-wrap; word-break: break-all; font-size: 12px; margin: 4px 0 8px; }
</style>
