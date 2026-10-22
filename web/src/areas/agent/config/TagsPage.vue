<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/admin/tags (FE-041, supervisor+): read-only tag list with usage
 * counts. Tags are created by applying them to tickets; the API offers no
 * rename/merge/delete endpoint yet (`GET /agent/tags` only).
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import { useI18n } from 'vue-i18n'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import DataTable from '@/components/DataTable.vue'

type S = components['schemas']
const items = ref<S['Tag'][]>([])
const loading = ref(true)
const failed = ref(false)
const q = ref('')
let timer: ReturnType<typeof setTimeout> | null = null
let seq = 0

const sorted = computed(() => [...items.value].sort((a, b) => (b.usage_count ?? 0) - (a.usage_count ?? 0) || a.name.localeCompare(b.name)))

async function load() {
  const mine = ++seq
  loading.value = true
  failed.value = false
  try {
    const query = q.value.trim() ? { q: q.value.trim() } : {}
    const res = (await unwrap(api.GET('/agent/tags', { params: { query } }))) ?? []
    if (mine === seq) items.value = res
  } catch {
    if (mine === seq) failed.value = true
  } finally {
    if (mine === seq) loading.value = false
  }
}
onMounted(load)
watch(q, () => {
  if (timer) clearTimeout(timer)
  timer = setTimeout(load, 300)
})
onBeforeUnmount(() => timer && clearTimeout(timer))
const { t } = useI18n()
const cols = useTableColumns('config-tags', () => [
  { key: 'name', label: t('agent.common.name'), locked: true },
  { key: 'usage', label: t('agent.tags.usage'), align: 'end', sortValue: (tag: S['Tag']) => tag.usage_count ?? 0 },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('agent.admin.nav.tags')" :subtitle="$t('agent.tags.subtitle')" />
    <v-alert type="info" variant="tonal" density="compact" class="mb-4">{{ $t('agent.tags.readOnlyHint') }}</v-alert>
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-text-field v-model="q" :label="$t('agent.common.search')" density="compact" hide-details clearable class="mb-3" style="max-width: 360px" data-testid="tags-search" />
    <v-card border flat>
      <v-progress-linear v-if="loading" indeterminate />
      <EmptyState v-if="!loading && !sorted.length" :title="$t('agent.tags.empty')" body="" />
      <template v-else>
      <ColumnsMenu :cols="cols" />
      <DataTable :cols="cols" :items="sorted" :loading="loading" density="comfortable" :row-props="{ 'data-testid': 'tag-row' }" data-testid="tags-table">
        <template #[`item.name`]="{ item }"><v-chip size="small" label>{{ item.name }}</v-chip></template>
        <template #[`item.usage`]="{ item }">{{ item.usage_count ?? 0 }}</template>
      </DataTable>
      </template>
    </v-card>
  </v-container>
</template>
