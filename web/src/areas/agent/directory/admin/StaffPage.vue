<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/** /agent/admin/staff (FE-027, admin): staff list, create local staff, edit role/tier/teams/status. */
import { onMounted, ref, watch } from 'vue'
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
import type { components, operations } from '@/api/schema'
import UserAvatar from '@/components/UserAvatar.vue'
import DataTable from '@/components/DataTable.vue'

type S = components['schemas']
const lookups = useLookupsStore()
const cp = useConfigProjects()
const q = ref('')
const role = ref<S['StaffRole'] | null>(null)
type StaffQuery = NonNullable<operations['listStaff']['parameters']['query']>
const sort = ref<NonNullable<StaffQuery['sort']> | null>(null)
const dialog = ref(false)
const editing = ref<S['User'] | null>(null)
const ROLES: S['StaffRole'][] = ['agent', 'supervisor', 'admin']
const list = useCursorList((cursor) =>
  unwrap(
    api.GET('/admin/staff', {
      params: { query: { q: q.value.trim() || undefined, role: role.value ?? undefined, sort: sort.value ?? undefined, cursor: cursor ?? undefined, limit: 50 } },
    }),
  ),
)
watch(sort, () => void list.reload())
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
  { key: 'name', label: t('agent.common.name'), locked: true, sort: { asc: 'name', desc: '-name' } },
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
      <DataTable
        :cols="cols"
        :items="list.items.value"
        :loading="list.loading.value"
        server-sort
        :sort="sort"
        :has-more="!!list.next.value"
        actions
        density="comfortable"
        :row-props="{ 'data-testid': 'staff-row' }"
        data-testid="staff-table"
        @update:sort="(v: string | null) => (sort = v as typeof sort)"
        @load-more="list.loadMore()"
      >
        <template #[`item.name`]="{ item }">
          <div class="d-flex align-center ga-2">
            <UserAvatar :name="item.name" :size="32" />
            <div>
              <div>{{ item.name }}</div>
              <div class="text-caption text-medium-emphasis">{{ item.email }}</div>
            </div>
          </div>
        </template>
        <template #[`item.role`]="{ item }">{{ $t(`agent.staff.roles.${item.role}`) }}</template>
        <template #[`item.tier`]="{ item }"><TierChip v-if="item.tier" :tier="item.tier" /></template>
        <template #[`item.projects`]="{ item }"><ProjectCell :ids="(item.projects ?? []).map((m: S['ProjectMembership']) => m.project_id)" /></template>
        <template #[`item.teams`]="{ item }">{{ teamNames(item.team_ids) || '—' }}</template>
        <template #[`item.status`]="{ item }">
          <v-chip size="small" label :color="item.status === 'active' ? 'success' : 'error'">
            {{ item.status === 'active' ? $t('agent.common.active') : $t('agent.common.disabled') }}
          </v-chip>
          <v-chip size="small" label variant="outlined" class="ml-1" data-testid="staff-source">
            {{ item.idp_managed ? $t('agent.staff.sso') : $t('agent.staff.local') }}
          </v-chip>
          <v-chip v-if="item.break_glass" size="small" label color="warning" class="ml-1">{{ $t('agent.staff.breakGlassShort') }}</v-chip>
        </template>
        <template #[`item.lastLogin`]="{ item }"><RelativeTime v-if="item.last_login_at" :value="item.last_login_at" /><span v-else>{{ $t('agent.common.never') }}</span></template>
        <template #[`item.actions`]="{ item }">
          <v-btn :icon="mdiPencil" variant="text" size="small" :aria-label="$t('agent.common.edit')" data-testid="staff-edit" @click="openForm(item)" />
        </template>
      </DataTable>
      </template>
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
