<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/admin/canned (FE-041, supervisor+): canned responses CRUD with team scope. */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiDelete, mdiPencil, mdiPlus } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import RelativeTime from '@/components/RelativeTime.vue'
import CannedForm from './CannedForm.vue'
import { api, unwrap } from '@/api/client'
import { useConfirm } from '@/composables/useConfirm'
import { useLookupsStore } from '../shared/lookups'
import ProjectCell from '../shared/ProjectCell.vue'
import { useConfigProjects } from '../shared/useConfigProjects'
import { useAction } from '../shared/useAction'
import type { components } from '@/api/schema'
import { useProjectsStore } from '@/stores/projects'
import DataTable from '@/components/DataTable.vue'

type S = components['schemas']
const { t } = useI18n()
const { confirm } = useConfirm()
const lookups = useLookupsStore()
const cp = useConfigProjects()
const projects = useProjectsStore()
const { run } = useAction()
const items = ref<S['CannedResponse'][]>([])
const loading = ref(true)
const failed = ref(false)
const dialog = ref(false)
const editing = ref<S['CannedResponse'] | null>(null)
const q = ref('')

const filtered = computed(() => {
  const s = q.value.trim().toLowerCase()
  const list = [...items.value].sort((a, b) => a.title.localeCompare(b.title))
  return s ? list.filter((c) => c.title.toLowerCase().includes(s) || c.body_md.toLowerCase().includes(s)) : list
})

async function load() {
  loading.value = true
  failed.value = false
  try {
    items.value = (await unwrap(api.GET('/admin/canned-responses'))) ?? []
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

function openForm(c: S['CannedResponse'] | null) {
  editing.value = c
  dialog.value = true
}
function onSaved() {
  dialog.value = false
  void load()
}
async function remove(c: S['CannedResponse']) {
  if (!(await confirm({ body: t('agent.common.confirmDelete', { name: c.title }), color: 'error' }))) return
  const ok = await run(async () => {
    await unwrap(api.DELETE('/admin/canned-responses/{id}', { params: { path: { id: c.id } } }))
    return true
  }, t('agent.common.deleted'))
  if (ok) void load()
}
const scopeLabel = (c: S['CannedResponse']) => (c.team_id ? lookups.teamName(c.team_id) || '—' : t('agent.canned.global'))
const changedAt = (c: S['CannedResponse']) => c.updated_at ?? c.created_at
/** What the project column shows (ProjectCell): the project key, or "all projects". */
const projectKey = (id?: string | null) => (id ? (projects.byId(id)?.key ?? '?') : t('projects.config.all'))
const cols = useTableColumns('config-canned', () => [
  { key: 'title', label: t('agent.canned.title'), locked: true },
  { key: 'project', label: t('projects.config.project'), lowPriority: true, sortValue: (c: S['CannedResponse']) => projectKey(c.project_id) },
  { key: 'scope', label: t('agent.canned.scope'), sortValue: scopeLabel },
  { key: 'updatedAt', label: t('agent.common.updatedAt'), lowPriority: true, sortValue: (c: S['CannedResponse']) => changedAt(c) ?? '' },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('agent.admin.nav.canned')" :subtitle="$t('agent.canned.subtitle')">
      <template #actions>
        <v-btn color="primary" :prepend-icon="mdiPlus" data-testid="canned-new" @click="openForm(null)">{{ $t('agent.canned.new') }}</v-btn>
      </template>
    </PageHeader>
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-text-field v-model="q" :label="$t('agent.common.search')" density="compact" hide-details clearable class="mb-3" style="max-width: 360px" />
    <v-card border flat>
      <v-progress-linear v-if="loading" indeterminate />
      <EmptyState v-if="!loading && !filtered.length" :title="$t('agent.canned.empty')" body="" />
      <template v-else>
      <ColumnsMenu :cols="cols" />
      <DataTable :cols="cols" :items="filtered" :loading="loading" actions density="comfortable" :row-props="{ 'data-testid': 'canned-row' }" data-testid="canned-table">
        <template #[`item.title`]="{ item }">{{ item.title }}</template>
        <template #[`item.project`]="{ item }"><ProjectCell :id="item.project_id" /></template>
        <template #[`item.scope`]="{ item }">{{ scopeLabel(item) }}</template>
        <template #[`item.updatedAt`]="{ item }"><RelativeTime v-if="changedAt(item)" :value="changedAt(item)!" /></template>
        <template #[`item.actions`]="{ item }">
          <span class="text-no-wrap">
            <v-btn :icon="mdiPencil" variant="text" size="small" :aria-label="$t('agent.common.edit')" @click="openForm(item)" />
            <v-btn :icon="mdiDelete" variant="text" size="small" :aria-label="$t('agent.common.delete')" data-testid="canned-delete" @click="remove(item)" />
          </span>
        </template>
      </DataTable>
      </template>
    </v-card>

    <v-dialog v-model="dialog" max-width="720">
      <v-card :title="editing ? $t('agent.canned.edit') : $t('agent.canned.new')">
        <v-card-text>
          <CannedForm v-if="dialog" :canned="editing" :teams="lookups.teams" :projects="cp.options.value" :can-global="cp.canGlobal.value" @saved="onSaved" @cancel="dialog = false" />
        </v-card-text>
      </v-card>
    </v-dialog>
  </v-container>
</template>
