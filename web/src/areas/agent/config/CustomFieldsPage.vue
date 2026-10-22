<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/admin/custom-fields (FE-053, admin): definitions list, create/edit, archive instead of delete. */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiArchiveArrowDown, mdiArchiveArrowUp, mdiPencil, mdiPlus } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import CustomFieldForm from './CustomFieldForm.vue'
import { api, unwrap } from '@/api/client'
import { useConfigStore } from '@/stores/config'
import { localized } from '@/utils/localized'
import { useLookupsStore } from '../shared/lookups'
import ProjectCell from '../shared/ProjectCell.vue'
import { useConfigProjects } from '../shared/useConfigProjects'
import { useAction } from '../shared/useAction'
import type { components } from '@/api/schema'

type S = components['schemas']
const { t, locale } = useI18n()
const config = useConfigStore()
const lookups = useLookupsStore()
const cp = useConfigProjects()
const { run } = useAction()
const items = ref<S['CustomFieldDef'][]>([])
const loading = ref(true)
const failed = ref(false)
const dialog = ref(false)
const editing = ref<S['CustomFieldDef'] | null>(null)
const showArchived = ref(false)

const shown = computed(() =>
  [...items.value]
    .filter((f) => showArchived.value || f.active)
    .sort((a, b) => a.sort - b.sort || a.key.localeCompare(b.key)),
)
const label = (f: S['CustomFieldDef']) => localized(f.label, locale.value, config.config.branding.default_locale ?? 'en') || f.key
const categoryNames = (f: S['CustomFieldDef']) =>
  f.category_ids?.length ? f.category_ids.map((id) => lookups.categoryName(id) || id).join(', ') : t('agent.customFields.allCategories')

async function load() {
  loading.value = true
  failed.value = false
  try {
    items.value = (await unwrap(api.GET('/admin/custom-fields'))) ?? []
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(() => {
  void load()
  void lookups.ensure('categories')
})

function openForm(f: S['CustomFieldDef'] | null) {
  editing.value = f
  dialog.value = true
}
function onSaved() {
  dialog.value = false
  lookups.invalidate('customFields')
  void load()
}
async function setActive(f: S['CustomFieldDef'], active: boolean) {
  const res = await run(
    () => unwrap(api.PATCH('/admin/custom-fields/{id}', { params: { path: { id: f.id } }, body: { active } })),
    active ? t('agent.customFields.reactivated') : t('agent.customFields.archived'),
  )
  if (res) {
    Object.assign(f, res)
    lookups.invalidate('customFields')
  }
}
const cols = useTableColumns('config-custom-fields', () => [
  { key: 'key', label: t('agent.customFields.key'), locked: true },
  { key: 'label', label: t('agent.customFields.label') },
  { key: 'type', label: t('agent.customFields.type'), lowPriority: true },
  { key: 'projects', label: t('projects.config.projects'), lowPriority: true },
  { key: 'categories', label: t('agent.customFields.categories'), lowPriority: true },
  { key: 'flags', label: t('agent.customFields.flags'), lowPriority: true },
  { key: 'status', label: t('agent.common.status') },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('agent.admin.nav.customFields')" :subtitle="$t('agent.customFields.subtitle')">
      <template #actions>
        <v-switch v-model="showArchived" :label="$t('agent.customFields.showArchived')" hide-details density="compact" inset class="mr-2" />
        <v-btn color="primary" :prepend-icon="mdiPlus" data-testid="cf-new" @click="openForm(null)">{{ $t('agent.customFields.new') }}</v-btn>
      </template>
    </PageHeader>
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="loading" indeterminate />
      <EmptyState v-if="!loading && !shown.length" :title="$t('agent.customFields.empty')" body="" />
      <template v-else>
      <ColumnsMenu :cols="cols" />
      <v-table density="comfortable" data-testid="cf-table">
        <thead>
          <tr>
            <th v-if="cols.show('key')">{{ $t('agent.customFields.key') }}</th>
            <th v-if="cols.show('label')">{{ $t('agent.customFields.label') }}</th>
            <th v-if="cols.show('type')">{{ $t('agent.customFields.type') }}</th>
            <th v-if="cols.show('projects')">{{ $t('projects.config.projects') }}</th>
            <th v-if="cols.show('categories')">{{ $t('agent.customFields.categories') }}</th>
            <th v-if="cols.show('flags')">{{ $t('agent.customFields.flags') }}</th>
            <th v-if="cols.show('status')">{{ $t('agent.common.status') }}</th>
            <th class="text-right">{{ $t('agent.common.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="f in shown" :key="f.id" data-testid="cf-row">
            <td v-if="cols.show('key')"><code>{{ f.key }}</code></td>
            <td v-if="cols.show('label')">{{ label(f) }}</td>
            <td v-if="cols.show('type')">{{ $t(`agent.customFields.types.${f.type}`) }}</td>
            <td v-if="cols.show('projects')"><ProjectCell :ids="f.project_ids ?? []" /></td>
            <td v-if="cols.show('categories')" class="text-caption">{{ categoryNames(f) }}</td>
            <td v-if="cols.show('flags')">
              <v-chip v-if="f.required" size="x-small" label class="mr-1">{{ $t('agent.customFields.required') }}</v-chip>
              <v-chip v-if="f.customer_visible" size="x-small" label color="info" class="mr-1">{{ $t('agent.customFields.visibleChip') }}</v-chip>
              <v-chip v-if="f.customer_editable" size="x-small" label color="info">{{ $t('agent.customFields.editableChip') }}</v-chip>
            </td>
            <td v-if="cols.show('status')">
              <v-chip size="small" label :color="f.active ? 'success' : undefined">{{ f.active ? $t('agent.common.active') : $t('agent.customFields.archivedChip') }}</v-chip>
            </td>
            <td class="text-right text-no-wrap">
              <v-btn :icon="mdiPencil" variant="text" size="small" :aria-label="$t('agent.common.edit')" data-testid="cf-edit" @click="openForm(f)" />
              <v-btn
                v-if="f.active"
                :icon="mdiArchiveArrowDown"
                variant="text"
                size="small"
                :aria-label="$t('agent.customFields.archive')"
                data-testid="cf-archive"
                @click="setActive(f, false)"
              />
              <v-btn
                v-else
                :icon="mdiArchiveArrowUp"
                variant="text"
                size="small"
                :aria-label="$t('agent.customFields.reactivate')"
                data-testid="cf-reactivate"
                @click="setActive(f, true)"
              />
            </td>
          </tr>
        </tbody>
      </v-table>
      </template>
    </v-card>

    <v-dialog v-model="dialog" max-width="720" scrollable>
      <v-card :title="editing ? $t('agent.customFields.edit') : $t('agent.customFields.new')">
        <v-card-text>
          <CustomFieldForm v-if="dialog" :field="editing" :categories="lookups.categories" :projects="cp.options.value" @saved="onSaved" @cancel="dialog = false" />
        </v-card-text>
      </v-card>
    </v-dialog>
  </v-container>
</template>
