<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/admin/teams (FE-028, supervisor+): teams CRUD, members, notifications, calendar. */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiAccountGroup, mdiAccountGroupOutline, mdiDelete, mdiPencil, mdiPlus } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import TeamForm from './TeamForm.vue'
import TeamMembers from './TeamMembers.vue'
import { api, unwrap } from '@/api/client'
import { useConfirm } from '@/composables/useConfirm'
import { useAction } from '../../shared/useAction'
import { useLookupsStore } from '../../shared/lookups'
import type { components } from '@/api/schema'
import SectionIcon from '@/components/SectionIcon.vue'

type S = components['schemas']
const { t } = useI18n()
const { confirm } = useConfirm()
const { run } = useAction()
const lookups = useLookupsStore()
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
      <v-table v-else density="comfortable" data-testid="team-table">
        <thead>
          <tr>
            <th>{{ $t('agent.common.name') }}</th>
            <th>{{ $t('agent.teams.members') }}</th>
            <th>{{ $t('agent.teams.notifyMode') }}</th>
            <th>{{ $t('agent.teams.calendar') }}</th>
            <th class="text-right">{{ $t('agent.common.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="team in teams" :key="team.id" data-testid="team-row">
            <td>
              <div class="d-flex align-center ga-2">
                <SectionIcon section="teams" :icon="mdiAccountGroupOutline" :size="30" />
                <div>
                  <div>{{ team.name }}</div>
                  <div v-if="team.description" class="text-caption text-medium-emphasis">{{ team.description }}</div>
                </div>
              </div>
            </td>
            <td>{{ team.member_count ?? '—' }}</td>
            <td>
              {{ $t(`agent.teams.modes.${team.notify_mode}`) }}
              <div v-if="team.notification_email" class="text-caption text-medium-emphasis">{{ team.notification_email }}</div>
            </td>
            <td>{{ calendarName(team.calendar_id) }}</td>
            <td class="text-right text-no-wrap">
              <v-btn :icon="mdiAccountGroup" variant="text" size="small" :aria-label="$t('agent.teams.members')" data-testid="team-members-open" @click="openMembers(team)" />
              <v-btn :icon="mdiPencil" variant="text" size="small" :aria-label="$t('agent.common.edit')" data-testid="team-edit" @click="openForm(team)" />
              <v-btn :icon="mdiDelete" variant="text" size="small" :aria-label="$t('agent.common.delete')" data-testid="team-delete" @click="remove(team)" />
            </td>
          </tr>
        </tbody>
      </v-table>
    </v-card>

    <v-dialog v-model="dialog" max-width="560">
      <v-card :title="editing ? $t('agent.teams.edit') : $t('agent.teams.new')">
        <v-card-text>
          <TeamForm v-if="dialog" :team="editing" :calendars="calendars" @saved="onSaved" @cancel="dialog = false" />
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
