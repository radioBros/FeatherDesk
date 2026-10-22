<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/**
 * /agent/admin/categories (FE-028, supervisor+): list, create/edit in a
 * dialog, active toggle, drag-to-sort (each move PATCHes the new `sort`).
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiDragVertical, mdiPencil, mdiPlus } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import PriorityChip from '@/components/PriorityChip.vue'
import CategoryForm from './CategoryForm.vue'
import ProjectCell from '../../shared/ProjectCell.vue'
import { useConfigProjects } from '../../shared/useConfigProjects'
import { api, unwrap } from '@/api/client'
import { useLookupsStore } from '../../shared/lookups'
import { useAction } from '../../shared/useAction'
import type { components } from '@/api/schema'
import DataTable from '@/components/DataTable.vue'
import { PRIORITIES } from '../../config/constants'

type S = components['schemas']
const { t } = useI18n()
const lookups = useLookupsStore()
const { run } = useAction()
const cp = useConfigProjects()
const projectFilter = ref<string | null>(null)
const items = ref<S['Category'][]>([])
const loading = ref(true)
const failed = ref(false)
const editing = ref<S['Category'] | null>(null)
const dialog = ref(false)
const dragIndex = ref<number | null>(null)

const sorted = computed(() => [...items.value].filter((c) => !projectFilter.value || c.project_id === projectFilter.value).sort((a, b) => a.sort - b.sort || a.name.localeCompare(b.name)))

async function load() {
  loading.value = true
  failed.value = false
  try {
    items.value = (await unwrap(api.GET('/admin/categories'))) ?? []
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(() => {
  void load()
  void lookups.ensure('teams')
})

function openForm(c: S['Category'] | null) {
  editing.value = c
  dialog.value = true
}
function onSaved() {
  dialog.value = false
  lookups.invalidate('categories')
  void load()
}

async function toggle(c: S['Category']) {
  const res = await run(() =>
    unwrap(api.PATCH('/admin/categories/{id}', { params: { path: { id: c.id } }, body: { name: c.name, active: !c.active } })),
  )
  if (res) Object.assign(c, res)
}

async function drop(to: number) {
  const from = dragIndex.value
  dragIndex.value = null
  if (from === null || from === to) return
  const list = [...sorted.value]
  const [moved] = list.splice(from, 1)
  list.splice(to, 0, moved!)
  // Renumber and persist only the rows whose position changed.
  const changed = list.map((c, i) => ({ c, sort: (i + 1) * 10 })).filter(({ c, sort }) => c.sort !== sort)
  for (const { c, sort } of changed) c.sort = sort
  await run(async () => {
    for (const { c, sort } of changed)
      await unwrap(api.PATCH('/admin/categories/{id}', { params: { path: { id: c.id } }, body: { name: c.name, sort } }))
  }, t('agent.common.saved'))
  lookups.invalidate('categories')
}
// Drag-to-sort goes by position in `sorted` (the saved order), whatever header sort is shown.
const indexOf = (c: S['Category']) => sorted.value.findIndex((x) => x.id === c.id)
const rowProps = ({ item }: { item: S['Category'] }) => ({
  draggable: 'true',
  'data-testid': 'category-row',
  onDragstart: () => (dragIndex.value = indexOf(item)),
  onDragover: (e: DragEvent) => e.preventDefault(),
  onDrop: (e: DragEvent) => {
    e.preventDefault()
    void drop(indexOf(item))
  },
})
const cols = useTableColumns('admin-categories', () => [
  { key: 'name', label: t('agent.common.name'), locked: true },
  { key: 'project', label: t('projects.config.project'), lowPriority: true, sortValue: (c: S['Category']) => cp.byId(c.project_id)?.key ?? '' },
  { key: 'team', label: t('agent.categories.defaultTeam'), lowPriority: true, sortValue: (c: S['Category']) => lookups.teamName(c.default_team_id) || '' },
  { key: 'priority', label: t('agent.categories.defaultPriority'), lowPriority: true, sortValue: (c: S['Category']) => (c.default_priority ? PRIORITIES.indexOf(c.default_priority) : -1) },
  { key: 'active', label: t('agent.common.active') },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('agent.admin.nav.categories')" :subtitle="$t('agent.categories.subtitle')">
      <template #actions>
        <v-btn color="primary" :prepend-icon="mdiPlus" data-testid="category-new" @click="openForm(null)">{{ $t('agent.categories.new') }}</v-btn>
      </template>
    </PageHeader>
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="loading" indeterminate />
      <EmptyState v-if="!loading && !sorted.length" :title="$t('agent.categories.empty')" body="" />
      <template v-else>
      <div class="d-flex align-center justify-space-between px-2 pt-1">
        <AutocompleteSearch
          v-if="cp.all.value.length > 1"
          v-model="projectFilter"
          :items="cp.all.value"
          item-title="name"
          item-value="id"
          :label="$t('projects.config.filter')"
          density="compact"
          variant="outlined"
          hide-details
          clearable
          max-width="260"
          data-testid="category-project-filter"
        />
        <v-spacer />
        <ColumnsMenu :cols="cols" :bar="false" />
      </div>
      <DataTable
        :cols="cols"
        :items="sorted"
        :loading="loading"
        actions
        density="comfortable"
        :row-props="rowProps"
        data-testid="category-table"
      >
        <template #[`item.name`]="{ item }">
          <div class="d-flex align-center ga-1">
            <v-icon class="fd-handle" :icon="mdiDragVertical" :aria-label="$t('agent.categories.dragToSort')" />
            <span>{{ item.name }}</span>
          </div>
        </template>
        <template #[`item.project`]="{ item }"><ProjectCell :id="item.project_id" /></template>
        <template #[`item.team`]="{ item }">{{ lookups.teamName(item.default_team_id) || '—' }}</template>
        <template #[`item.priority`]="{ item }"><PriorityChip v-if="item.default_priority" :priority="item.default_priority" /><span v-else>—</span></template>
        <template #[`item.active`]="{ item }"><v-switch :model-value="item.active" hide-details density="compact" color="primary" inset :aria-label="$t('agent.common.active')" @update:model-value="toggle(item)" /></template>
        <template #[`item.actions`]="{ item }">
          <v-btn :icon="mdiPencil" variant="text" size="small" :aria-label="$t('agent.common.edit')" @click="openForm(item)" />
        </template>
      </DataTable>
      </template>
    </v-card>

    <v-dialog v-model="dialog" max-width="520">
      <v-card :title="editing ? $t('agent.categories.edit') : $t('agent.categories.new')">
        <v-card-text>
          <CategoryForm v-if="dialog" :category="editing" :teams="lookups.teams" :projects="cp.options.value" @saved="onSaved" @cancel="dialog = false" />
        </v-card-text>
      </v-card>
    </v-dialog>
  </v-container>
</template>

<style scoped>
.fd-handle { cursor: grab; }
</style>
