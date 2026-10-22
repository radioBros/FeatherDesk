<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/admin/teams (FE-028, supervisor+): teams CRUD, members, notifications, calendar. */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiAccountGroup, mdiAccountGroupOutline, mdiDelete, mdiPencil, mdiPlus } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import TeamForm from './TeamForm.vue'
import TeamMembers from './TeamMembers.vue'
import ProjectCell from '../../shared/ProjectCell.vue'
import { useConfigProjects } from '../../shared/useConfigProjects'
import { api, unwrap } from '@/api/client'
import { useConfirm } from '@/composables/useConfirm'
import { useAction } from '../../shared/useAction'
import { useLookupsStore } from '../../shared/lookups'
import type { components } from '@/api/schema'
import SectionIcon from '@/components/SectionIcon.vue'
import DataTable from '@/components/DataTable.vue'

type S = components['schemas']
const { t } = useI18n()
const { confirm } = useConfirm()
const { run } = useAction()
const lookups = useLookupsStore()
const cp = useConfigProjects()
const teams = ref<S['Team'][]>([])
const calendars = ref<S['BusinessCalendar'][]>([])
const staff = ref<S['User'][]>([])
const loading = ref(true)
const failed = ref(false)
const dialog = ref(false)
const editing = ref<S['Team'] | null>(null)
const membersOf = ref<S['Team'] | null>(null)

async function load() {
  loading.value = true
  failed.value = false
  try {
    teams.value = (await unwrap(api.GET('/admin/teams'))) ?? []
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
async function loadCalendars() {
  try {
    calendars.value = (await unwrap(api.GET('/admin/calendars'))) ?? []
  } catch {
    calendars.value = []
  }
}
async function loadStaff() {
  try {
    staff.value = (await unwrap(api.GET('/admin/staff', { params: { query: { limit: 200 } } }))).items ?? []
  } catch {
    staff.value = []
  }
}
onMounted(() => {
  void load()
  void loadCalendars()
})
const calendarName = (id?: string | null) => (id ? calendars.value.find((c) => c.id === id)?.name ?? '' : t('agent.teams.calendar247'))

function openForm(team: S['Team'] | null) {
  editing.value = team
  dialog.value = true
}
function onSaved() {
  dialog.value = false
  lookups.invalidate('teams')
  void load()
}
function openMembers(team: S['Team']) {
  membersOf.value = team
  if (!staff.value.length) void loadStaff()
}
async function remove(team: S['Team']) {
  if (!(await confirm({ body: t('agent.common.confirmDelete', { name: team.name }), color: 'error', confirmText: t('agent.common.delete') }))) return
  const ok = await run(async () => {
    await unwrap(api.DELETE('/admin/teams/{id}', { params: { path: { id: team.id } } }))
    return true
  }, t('agent.common.deleted'))
  if (ok) {
    lookups.invalidate('teams')
    void load()
  }
}
const cols = useTableColumns('admin-teams', () => [
  { key: 'name', label: t('agent.common.name'), locked: true },
  { key: 'projects', label: t('projects.config.projects'), lowPriority: true, sortValue: false as const },
  { key: 'members', label: t('agent.teams.members'), lowPriority: true, sortValue: (team: S['Team']) => team.member_count ?? -1 },
  { key: 'notifyMode', label: t('agent.teams.notifyMode'), lowPriority: true, sortValue: (team: S['Team']) => t(`agent.teams.modes.${team.notify_mode}`) },
  { key: 'calendar', label: t('agent.teams.calendar'), lowPriority: true, sortValue: (team: S['Team']) => calendarName(team.calendar_id) },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('agent.admin.nav.teams')" :subtitle="$t('agent.teams.subtitle')">
      <template #actions>
        <v-btn color="primary" :prepend-icon="mdiPlus" data-testid="team-new" @click="openForm(null)">{{ $t('agent.teams.new') }}</v-btn>
      </template>
    </PageHeader>
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="loading" indeterminate />
      <EmptyState v-if="!loading && !teams.length" :title="$t('agent.teams.empty')" body="" />
      <template v-else>
      <ColumnsMenu :cols="cols" />
      <DataTable
        :cols="cols"
        :items="teams"
        :loading="loading"
        actions
        density="comfortable"
        :row-props="{ 'data-testid': 'team-row' }"
        data-testid="team-table"
      >
        <template #[`item.name`]="{ item }">
          <div class="d-flex align-center ga-2">
            <SectionIcon section="teams" :icon="mdiAccountGroupOutline" :size="30" />
            <div>
              <div>{{ item.name }}</div>
              <div v-if="item.description" class="text-caption text-medium-emphasis">{{ item.description }}</div>
            </div>
          </div>
        </template>
        <template #[`item.projects`]="{ item }"><ProjectCell :ids="item.project_ids ?? []" /></template>
        <template #[`item.members`]="{ item }">{{ item.member_count ?? '—' }}</template>
        <template #[`item.notifyMode`]="{ item }">
          {{ $t(`agent.teams.modes.${item.notify_mode}`) }}
          <div v-if="item.notification_email" class="text-caption text-medium-emphasis">{{ item.notification_email }}</div>
        </template>
        <template #[`item.calendar`]="{ item }">{{ calendarName(item.calendar_id) }}</template>
        <template #[`item.actions`]="{ item }">
          <span class="text-no-wrap">
            <v-btn :icon="mdiAccountGroup" variant="text" size="small" :aria-label="$t('agent.teams.members')" data-testid="team-members-open" @click="openMembers(item)" />
            <v-btn :icon="mdiPencil" variant="text" size="small" :aria-label="$t('agent.common.edit')" data-testid="team-edit" @click="openForm(item)" />
            <v-btn :icon="mdiDelete" variant="text" size="small" :aria-label="$t('agent.common.delete')" data-testid="team-delete" @click="remove(item)" />
          </span>
        </template>
      </DataTable>
      </template>
    </v-card>

    <v-dialog v-model="dialog" max-width="560">
      <v-card :title="editing ? $t('agent.teams.edit') : $t('agent.teams.new')">
        <v-card-text>
          <TeamForm v-if="dialog" :team="editing" :calendars="calendars" :projects="cp.options.value" @saved="onSaved" @cancel="dialog = false" />
        </v-card-text>
      </v-card>
    </v-dialog>
    <v-dialog :model-value="!!membersOf" max-width="600" @update:model-value="(v: boolean) => !v && (membersOf = null)">
      <v-card v-if="membersOf" :title="$t('agent.teams.membersOf', { team: membersOf.name })">
        <v-card-text>
          <TeamMembers :team="membersOf" :staff="staff" @changed="load" />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="membersOf = null">{{ $t('app.close') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </v-container>
</template>
