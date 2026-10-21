<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/admin/categories (FE-028, supervisor+): list, create/edit in a
 * dialog, active toggle, drag-to-sort (each move PATCHes the new `sort`).
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiDragVertical, mdiPencil, mdiPlus } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import PriorityChip from '@/components/PriorityChip.vue'
import CategoryForm from './CategoryForm.vue'
import { api, unwrap } from '@/api/client'
import { useLookupsStore } from '../../shared/lookups'
import { useAction } from '../../shared/useAction'
import type { components } from '@/api/schema'

type S = components['schemas']
const { t } = useI18n()
const lookups = useLookupsStore()
const { run } = useAction()
const items = ref<S['Category'][]>([])
const loading = ref(true)
const failed = ref(false)
const editing = ref<S['Category'] | null>(null)
const dialog = ref(false)
const dragIndex = ref<number | null>(null)

const sorted = computed(() => [...items.value].sort((a, b) => a.sort - b.sort || a.name.localeCompare(b.name)))

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
      <v-table v-else density="comfortable" data-testid="category-table">
        <thead>
          <tr>
            <th class="st-handle" />
            <th>{{ $t('agent.common.name') }}</th>
            <th>{{ $t('agent.categories.defaultTeam') }}</th>
            <th>{{ $t('agent.categories.defaultPriority') }}</th>
            <th>{{ $t('agent.common.active') }}</th>
            <th class="text-right">{{ $t('agent.common.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="(c, i) in sorted"
            :key="c.id"
            draggable="true"
            data-testid="category-row"
            @dragstart="dragIndex = i"
            @dragover.prevent
            @drop.prevent="drop(i)"
          >
            <td class="st-handle"><v-icon :icon="mdiDragVertical" :aria-label="$t('agent.categories.dragToSort')" /></td>
            <td>{{ c.name }}</td>
            <td>{{ lookups.teamName(c.default_team_id) || '—' }}</td>
            <td><PriorityChip v-if="c.default_priority" :priority="c.default_priority" /><span v-else>—</span></td>
            <td><v-switch :model-value="c.active" hide-details density="compact" color="primary" inset :aria-label="$t('agent.common.active')" @update:model-value="toggle(c)" /></td>
            <td class="text-right">
              <v-btn :icon="mdiPencil" variant="text" size="small" :aria-label="$t('agent.common.edit')" @click="openForm(c)" />
            </td>
          </tr>
        </tbody>
      </v-table>
    </v-card>

    <v-dialog v-model="dialog" max-width="520">
      <v-card :title="editing ? $t('agent.categories.edit') : $t('agent.categories.new')">
        <v-card-text>
          <CategoryForm v-if="dialog" :category="editing" :teams="lookups.teams" @saved="onSaved" @cancel="dialog = false" />
        </v-card-text>
      </v-card>
    </v-dialog>
  </v-container>
</template>

<style scoped>
.st-handle { width: 32px; cursor: grab; }
</style>
