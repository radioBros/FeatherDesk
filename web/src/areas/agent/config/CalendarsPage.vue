<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/admin/calendars (FE-056, supervisor+): business-hours calendars used by team SLAs. */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiDelete, mdiPencil, mdiPlus } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import CalendarForm from './CalendarForm.vue'
import { api, unwrap } from '@/api/client'
import { useConfirm } from '@/composables/useConfirm'
import { useAction } from '../shared/useAction'
import type { components } from '@/api/schema'
import { WEEKDAYS } from './constants'
import ColumnCells from '@/components/ColumnCells.vue'

type S = components['schemas']
const { t } = useI18n()
const { confirm } = useConfirm()
const { run } = useAction()
const items = ref<S['BusinessCalendar'][]>([])
const loading = ref(true)
const failed = ref(false)
const dialog = ref(false)
const editing = ref<S['BusinessCalendar'] | null>(null)

const openDays = (c: S['BusinessCalendar']) => WEEKDAYS.filter((d) => (c.week[d] ?? []).length).length

async function load() {
  loading.value = true
  failed.value = false
  try {
    items.value = (await unwrap(api.GET('/admin/calendars'))) ?? []
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(load)

function openForm(c: S['BusinessCalendar'] | null) {
  editing.value = c
  dialog.value = true
}
function onSaved() {
  dialog.value = false
  void load()
}
async function remove(c: S['BusinessCalendar']) {
  if (!(await confirm({ body: t('agent.common.confirmDelete', { name: c.name }), color: 'error' }))) return
  const ok = await run(async () => {
    await unwrap(api.DELETE('/admin/calendars/{id}', { params: { path: { id: c.id } } }))
    return true
  }, t('agent.common.deleted'))
  if (ok) void load()
}
const cols = useTableColumns('config-calendars', () => [
  { key: 'name', label: t('agent.common.name'), locked: true },
  { key: 'timezone', label: t('agent.calendars.timezone') },
  { key: 'openDays', label: t('agent.calendars.openDays'), lowPriority: true },
  { key: 'holidays', label: t('agent.calendars.holidays'), lowPriority: true },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('agent.admin.nav.calendars')" :subtitle="$t('agent.calendars.subtitle')">
      <template #actions>
        <v-btn color="primary" :prepend-icon="mdiPlus" data-testid="calendar-new" @click="openForm(null)">{{ $t('agent.calendars.new') }}</v-btn>
      </template>
    </PageHeader>
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="loading" indeterminate />
      <EmptyState v-if="!loading && !items.length" :title="$t('agent.calendars.empty')" body="" />
      <template v-else>
      <ColumnsMenu :cols="cols" />
      <v-table density="comfortable" data-testid="calendar-table">
        <thead>
          <tr>
            <ColumnCells :cols="cols" head>
              <th data-col="name" v-if="cols.show('name')">{{ $t('agent.common.name') }}</th>
              <th data-col="timezone" v-if="cols.show('timezone')">{{ $t('agent.calendars.timezone') }}</th>
              <th data-col="openDays" v-if="cols.show('openDays')">{{ $t('agent.calendars.openDays') }}</th>
              <th data-col="holidays" v-if="cols.show('holidays')">{{ $t('agent.calendars.holidays') }}</th>
              <th class="text-right">{{ $t('agent.common.actions') }}</th>
            </ColumnCells>
          </tr>
        </thead>
        <tbody>
          <tr v-for="c in items" :key="c.id" data-testid="calendar-row">
            <ColumnCells :cols="cols">
              <td data-col="name" v-if="cols.show('name')">{{ c.name }}</td>
              <td data-col="timezone" v-if="cols.show('timezone')">{{ c.timezone }}</td>
              <td data-col="openDays" v-if="cols.show('openDays')">{{ openDays(c) }}</td>
              <td data-col="holidays" v-if="cols.show('holidays')">{{ c.holidays.length }}</td>
              <td class="text-right text-no-wrap">
                <v-btn :icon="mdiPencil" variant="text" size="small" :aria-label="$t('agent.common.edit')" @click="openForm(c)" />
                <v-btn :icon="mdiDelete" variant="text" size="small" :aria-label="$t('agent.common.delete')" data-testid="calendar-delete" @click="remove(c)" />
              </td>
            </ColumnCells>
          </tr>
        </tbody>
      </v-table>
      </template>
    </v-card>

    <v-dialog v-model="dialog" max-width="760" scrollable>
      <v-card :title="editing ? $t('agent.calendars.edit') : $t('agent.calendars.new')">
        <v-card-text>
          <CalendarForm v-if="dialog" :calendar="editing" @saved="onSaved" @cancel="dialog = false" />
        </v-card-text>
      </v-card>
    </v-dialog>
  </v-container>
</template>
