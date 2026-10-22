<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/admin/projects (D-20): the projects list with column toggles, create
 * (supervisor+) and a detail dialog with Settings, Members, Teams and SLA tabs.
 * Project admins see only their own projects and cannot create, delete or
 * change key, active or sort; the API enforces it, the UI just hides.
 */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiDelete, mdiFolderMultipleOutline, mdiPencil, mdiPlus } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import ProjectChip from '@/components/ProjectChip.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import { useConfirm } from '@/composables/useConfirm'
import { api, unwrap } from '@/api/client'
import { useSessionStore } from '@/stores/session'
import { useProjectsStore } from '@/stores/projects'
import { useAction } from '../shared/useAction'
import { useLookupsStore } from '../shared/lookups'
import type { components } from '@/api/schema'
import ProjectForm from './ProjectForm.vue'
import ProjectMembers from './ProjectMembers.vue'
import ProjectTeams from './ProjectTeams.vue'
import ProjectSla from './ProjectSla.vue'
import ProjectLogo from './ProjectLogo.vue'
import DataTable from '@/components/DataTable.vue'

type S = components['schemas']
const { t } = useI18n()
const session = useSessionStore()
const projectsStore = useProjectsStore()
const lookups = useLookupsStore()
const { confirm } = useConfirm()
const { run } = useAction()

const projects = ref<S['Project'][]>([])
const loading = ref(true)
const failed = ref(false)
const dialog = ref(false)
const editing = ref<S['Project'] | null>(null)
const tab = ref('settings')
const members = ref<S['ProjectMember'][]>([])
const staff = ref<S['User'][]>([])
const sla = ref<S['ProjectSlaPolicy'][]>([])
const detailLoading = ref(false)
/** Bumped to remount the tab panels after a reload. */
const version = ref(0)

async function load() {
  loading.value = true
  failed.value = false
  try {
    projects.value = (await unwrap(api.GET('/admin/projects'))) ?? []
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

const teamName = (id?: string | null) => (id ? lookups.teamName(id) : '')

function openCreate() {
  editing.value = null
  tab.value = 'settings'
  dialog.value = true
}
async function openEdit(p: S['Project']) {
  editing.value = p
  tab.value = 'settings'
  dialog.value = true
  detailLoading.value = true
  try {
    const [m, s, st] = await Promise.all([
      unwrap(api.GET('/admin/projects/{id}/members', { params: { path: { id: p.id } } })).catch(() => []),
      unwrap(api.GET('/admin/projects/{id}/sla-policies', { params: { path: { id: p.id } } })).catch(() => []),
      session.isSupervisor && !staff.value.length
        ? unwrap(api.GET('/admin/staff', { params: { query: { limit: 200 } } })).then((r) => r.items ?? []).catch(() => [])
        : Promise.resolve(staff.value),
    ])
    members.value = m ?? []
    sla.value = s ?? []
    staff.value = st
    version.value++
  } finally {
    detailLoading.value = false
  }
}
async function refresh() {
  await load()
  void projectsStore.load(true)
}
async function onSaved(saved: S['Project']) {
  await refresh()
  if (editing.value) editing.value = projects.value.find((p) => p.id === saved.id) ?? saved
  else dialog.value = false
}
async function onLogo(p: S['Project']) {
  editing.value = p
  await refresh()
}
async function onTeams() {
  await refresh()
  if (editing.value) editing.value = projects.value.find((p) => p.id === editing.value!.id) ?? editing.value
}
async function remove(p: S['Project']) {
  if (!(await confirm({ body: t('agent.common.confirmDelete', { name: p.name }), color: 'error', confirmText: t('agent.common.delete') }))) return
  const ok = await run(async () => {
    await unwrap(api.DELETE('/admin/projects/{id}', { params: { path: { id: p.id } } }))
    return true
  }, t('agent.common.deleted'))
  if (ok) void refresh()
}

const cols = useTableColumns('admin-projects', () => [
  { key: 'key', label: t('projects.admin.key'), locked: true },
  { key: 'name', label: t('agent.common.name'), locked: true },
  { key: 'color', label: t('projects.admin.color'), lowPriority: true },
  { key: 'active', label: t('agent.common.status'), lowPriority: true },
  { key: 'portal', label: t('projects.admin.portalVisible'), lowPriority: true, sortValue: (p: S['Project']) => p.portal_visible },
  { key: 'members', label: t('agent.teams.members'), lowPriority: true, sortValue: (p: S['Project']) => p.member_count },
  { key: 'tickets', label: t('projects.admin.tickets'), lowPriority: true, sortValue: (p: S['Project']) => p.ticket_count },
  { key: 'defaultTeam', label: t('projects.admin.defaultTeam'), defaultHidden: true, sortValue: (p: S['Project']) => teamName(p.default_team_id) },
  { key: 'sort', label: t('projects.admin.sort'), defaultHidden: true },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('projects.nav.title')" :subtitle="$t('projects.admin.subtitle')">
      <template #actions>
        <v-btn v-if="session.isSupervisor" color="primary" :prepend-icon="mdiPlus" data-testid="project-new" @click="openCreate">{{ $t('projects.admin.new') }}</v-btn>
      </template>
    </PageHeader>
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="loading" indeterminate />
      <EmptyState v-if="!loading && !projects.length" :icon="mdiFolderMultipleOutline" :title="$t('projects.admin.empty')" :body="$t('projects.admin.emptyBody')" />
      <template v-else>
        <ColumnsMenu :cols="cols" />
        <DataTable
          :cols="cols"
          :items="projects"
          :loading="loading"
          actions
          density="compact"
          :row-props="{ 'data-testid': 'project-row' }"
          data-testid="project-table"
        >
          <template #[`item.key`]="{ item }"><ProjectChip :project="item" size="small" /></template>
          <template #[`item.name`]="{ item }">
            <div>{{ item.name }}</div>
            <div v-if="item.description" class="text-caption text-medium-emphasis">{{ item.description }}</div>
          </template>
          <template #[`item.color`]="{ item }"><span v-if="item.color" class="d-inline-flex align-center ga-1"><span class="fd-swatch" :style="{ background: item.color }" />{{ item.color }}</span></template>
          <template #[`item.active`]="{ item }">{{ item.active ? $t('agent.common.active') : $t('agent.common.inactive') }}</template>
          <template #[`item.portal`]="{ item }">{{ item.portal_visible ? $t('agent.common.yes') : $t('agent.common.no') }}</template>
          <template #[`item.members`]="{ item }">{{ item.member_count }}</template>
          <template #[`item.tickets`]="{ item }">{{ item.ticket_count }}</template>
          <template #[`item.defaultTeam`]="{ item }">{{ teamName(item.default_team_id) }}</template>
          <template #[`item.sort`]="{ item }">{{ item.sort }}</template>
          <template #[`item.actions`]="{ item }">
            <span class="text-no-wrap">
              <v-btn :icon="mdiPencil" variant="text" size="small" :aria-label="$t('agent.common.edit')" data-testid="project-edit" @click="openEdit(item)" />
              <v-btn v-if="session.isSupervisor" :icon="mdiDelete" variant="text" size="small" :aria-label="$t('agent.common.delete')" data-testid="project-delete" @click="remove(item)" />
            </span>
          </template>
        </DataTable>
      </template>
    </v-card>

    <v-dialog v-model="dialog" max-width="760" scrollable>
      <v-card :title="editing ? $t('projects.admin.edit', { name: editing.name }) : $t('projects.admin.new')" data-testid="project-dialog">
        <v-tabs v-if="editing" v-model="tab" density="compact" data-testid="project-tabs">
          <v-tab value="settings">{{ $t('projects.admin.tabs.settings') }}</v-tab>
          <v-tab value="members" data-testid="project-tab-members">{{ $t('projects.admin.tabs.members') }}</v-tab>
          <v-tab value="teams" data-testid="project-tab-teams">{{ $t('projects.admin.tabs.teams') }}</v-tab>
          <v-tab value="sla" data-testid="project-tab-sla">{{ $t('projects.admin.tabs.sla') }}</v-tab>
        </v-tabs>
        <v-card-text>
          <v-progress-linear v-if="editing && detailLoading" indeterminate />
          <template v-if="dialog && !(editing && detailLoading)">
            <v-window v-if="editing" v-model="tab" :touch="false">
              <v-window-item value="settings">
                <ProjectForm :key="`f${version}${editing.updated_at}`" :project="editing" :teams="lookups.teams" :support="session.isSupervisor" @saved="onSaved" @cancel="dialog = false" />
                <div class="text-subtitle-2 mt-4 mb-1">{{ $t('projects.admin.logo.title') }}</div>
                <ProjectLogo :project="editing" @changed="onLogo" />
              </v-window-item>
              <v-window-item value="members">
                <ProjectMembers :key="`m${version}`" :project-id="editing.id" :members="members" :staff="staff" :editable="session.isSupervisor" @saved="(m) => { members = m; void refresh() }" />
              </v-window-item>
              <v-window-item value="teams">
                <ProjectTeams
                  :key="`t${version}${editing.team_ids.length}`"
                  :project-id="editing.id"
                  :teams="lookups.teams"
                  :team-ids="editing.team_ids"
                  :default-team-id="editing.default_team_id"
                  editable
                  @saved="onTeams"
                />
              </v-window-item>
              <v-window-item value="sla">
                <ProjectSla :key="`s${version}`" :project-id="editing.id" :policies="sla" @saved="(s) => { sla = s; version++ }" />
              </v-window-item>
            </v-window>
            <ProjectForm v-else :teams="lookups.teams" support @saved="onSaved" @cancel="dialog = false" />
          </template>
        </v-card-text>
        <v-card-actions v-if="editing">
          <v-spacer />
          <v-btn variant="text" @click="dialog = false">{{ $t('app.close') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </v-container>
</template>

<style scoped>
.fd-swatch { display: inline-block; width: 12px; height: 12px; border-radius: 3px; border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity)); }
</style>
