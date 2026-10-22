<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/** /agent/admin/staff (FE-027, admin): staff list, create local staff, edit role/tier/teams/status. */
import { onMounted, ref } from 'vue'
import { mdiMagnify, mdiPencil, mdiPlus } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import { useI18n } from 'vue-i18n'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import TierChip from '@/components/TierChip.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import StaffForm from './StaffForm.vue'
import ProjectCell from '../../shared/ProjectCell.vue'
import { useConfigProjects } from '../../shared/useConfigProjects'
import PrivacyActions from '../../privacy/PrivacyActions.vue'
import UserMfaCard from './UserMfaCard.vue'
import { api, unwrap } from '@/api/client'
import { useCursorList } from '../../shared/useCursorList'
import { useLookupsStore } from '../../shared/lookups'
import type { components } from '@/api/schema'
import UserAvatar from '@/components/UserAvatar.vue'
import ColumnCells from '@/components/ColumnCells.vue'

type S = components['schemas']
const lookups = useLookupsStore()
const cp = useConfigProjects()
const q = ref('')
const role = ref<S['StaffRole'] | null>(null)
const dialog = ref(false)
const editing = ref<S['User'] | null>(null)
const ROLES: S['StaffRole'][] = ['agent', 'supervisor', 'admin']
const list = useCursorList((cursor) =>
  unwrap(
    api.GET('/admin/staff', {
      params: { query: { q: q.value.trim() || undefined, role: role.value ?? undefined, cursor: cursor ?? undefined, limit: 50 } },
    }),
  ),
)
let timer: ReturnType<typeof setTimeout> | undefined
function onSearch() {
  clearTimeout(timer)
  timer = setTimeout(() => void list.reload(), 300)
}
onMounted(() => {
  void list.reload()
  void lookups.ensure('teams')
})
function openForm(u: S['User'] | null) {
  editing.value = u
  dialog.value = true
}
function onSaved() {
  dialog.value = false
  lookups.invalidate('staff')
  void list.reload()
}
const teamNames = (ids?: string[]) => (ids ?? []).map((id) => lookups.teamName(id)).filter(Boolean).join(', ')
const { t } = useI18n()
const cols = useTableColumns('admin-staff', () => [
  { key: 'name', label: t('agent.common.name'), locked: true },
  { key: 'role', label: t('agent.staff.role') },
  { key: 'tier', label: t('tier.label'), lowPriority: true },
  { key: 'projects', label: t('projects.config.staffColumn'), lowPriority: true },
  { key: 'teams', label: t('agent.staff.teams'), lowPriority: true },
  { key: 'status', label: t('agent.common.status') },
  { key: 'lastLogin', label: t('agent.staff.lastLogin'), lowPriority: true },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('agent.admin.nav.staff')" :subtitle="$t('agent.staff.subtitle')">
      <template #actions>
        <v-btn color="primary" :prepend-icon="mdiPlus" data-testid="staff-new" @click="openForm(null)">{{ $t('agent.staff.new') }}</v-btn>
      </template>
    </PageHeader>
    <div class="d-flex flex-wrap ga-3 mb-4">
      <v-text-field
        v-model="q"
        :prepend-inner-icon="mdiMagnify"
        :label="$t('agent.common.search')"
        clearable
        hide-details
        density="compact"
        style="max-width: 360px"
        @update:model-value="onSearch"
      />
      <AutocompleteSearch
        v-model="role"
        :items="ROLES.map((r) => ({ value: r, title: $t(`agent.staff.roles.${r}`) }))"
        :label="$t('agent.staff.role')"
        clearable
        hide-details
        density="compact"
        style="max-width: 220px"
        @update:model-value="list.reload()"
      />
    </div>
    <v-alert v-if="list.error.value" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="list.loading.value" indeterminate />
      <EmptyState v-if="!list.loading.value && !list.items.value.length" :title="$t('agent.staff.empty')" body="" />
      <template v-else>
      <ColumnsMenu :cols="cols" />
      <v-table density="comfortable" data-testid="staff-table">
        <thead>
          <tr>
            <ColumnCells :cols="cols" head>
              <th data-col="name" v-if="cols.show('name')">{{ $t('agent.common.name') }}</th>
              <th data-col="role" v-if="cols.show('role')">{{ $t('agent.staff.role') }}</th>
              <th data-col="tier" v-if="cols.show('tier')">{{ $t('tier.label') }}</th>
              <th data-col="projects" v-if="cols.show('projects')">{{ $t('projects.config.staffColumn') }}</th>
              <th data-col="teams" v-if="cols.show('teams')">{{ $t('agent.staff.teams') }}</th>
              <th data-col="status" v-if="cols.show('status')">{{ $t('agent.common.status') }}</th>
              <th data-col="lastLogin" v-if="cols.show('lastLogin')">{{ $t('agent.staff.lastLogin') }}</th>
              <th class="text-right">{{ $t('agent.common.actions') }}</th>
            </ColumnCells>
          </tr>
        </thead>
        <tbody>
          <tr v-for="u in list.items.value" :key="u.id" data-testid="staff-row">
            <ColumnCells :cols="cols">
              <td data-col="name" v-if="cols.show('name')">
                <div class="d-flex align-center ga-2">
                  <UserAvatar :name="u.name" :size="32" />
                  <div>
                    <div>{{ u.name }}</div>
                    <div class="text-caption text-medium-emphasis">{{ u.email }}</div>
                  </div>
                </div>
              </td>
              <td data-col="role" v-if="cols.show('role')">{{ $t(`agent.staff.roles.${u.role}`) }}</td>
              <td data-col="tier" v-if="cols.show('tier')"><TierChip v-if="u.tier" :tier="u.tier" /></td>
              <td data-col="projects" v-if="cols.show('projects')"><ProjectCell :ids="(u.projects ?? []).map((m) => m.project_id)" /></td>
              <td data-col="teams" v-if="cols.show('teams')">{{ teamNames(u.team_ids) || '—' }}</td>
              <td data-col="status" v-if="cols.show('status')">
                <v-chip size="small" label :color="u.status === 'active' ? 'success' : 'error'">
                  {{ u.status === 'active' ? $t('agent.common.active') : $t('agent.common.disabled') }}
                </v-chip>
                <v-chip size="small" label variant="outlined" class="ml-1" data-testid="staff-source">
                  {{ u.idp_managed ? $t('agent.staff.sso') : $t('agent.staff.local') }}
                </v-chip>
                <v-chip v-if="u.break_glass" size="small" label color="warning" class="ml-1">{{ $t('agent.staff.breakGlassShort') }}</v-chip>
              </td>
              <td data-col="lastLogin" v-if="cols.show('lastLogin')"><RelativeTime v-if="u.last_login_at" :value="u.last_login_at" /><span v-else>{{ $t('agent.common.never') }}</span></td>
              <td class="text-right">
                <v-btn :icon="mdiPencil" variant="text" size="small" :aria-label="$t('agent.common.edit')" data-testid="staff-edit" @click="openForm(u)" />
              </td>
            </ColumnCells>
          </tr>
        </tbody>
      </v-table>
      </template>
      <div v-if="list.next.value" class="pa-2 text-center">
        <v-btn variant="text" :loading="list.loading.value" @click="list.loadMore()">{{ $t('agent.common.loadMore') }}</v-btn>
      </div>
    </v-card>
    <v-dialog v-model="dialog" max-width="600">
      <v-card :title="editing ? $t('agent.staff.edit') : $t('agent.staff.new')">
        <v-card-text>
          <StaffForm v-if="dialog" :staff="editing" :teams="lookups.teams" :projects="cp.all.value" @saved="onSaved" @cancel="dialog = false" />
          <PrivacyActions v-if="dialog && editing" :user="editing" @erased="onSaved" />
          <UserMfaCard v-if="dialog && editing" :user-id="editing.id" />
        </v-card-text>
      </v-card>
    </v-dialog>
  </v-container>
</template>
