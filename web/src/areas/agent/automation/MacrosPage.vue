<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Macros manager (FE-064, D-19 A).
 * - `/agent/macros`: the caller's personal macros (editable) plus the shared
 *   ones (read-only here).
 * - `/agent/admin/macros` (`shared`, supervisor+): the shared macros.
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiDelete, mdiPencil, mdiPlus } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import RelativeTime from '@/components/RelativeTime.vue'
import MacroForm from './MacroForm.vue'
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
const props = defineProps<{ shared?: boolean }>()
const { t } = useI18n()
const { confirm } = useConfirm()
const lookups = useLookupsStore()
const cp = useConfigProjects()
const projects = useProjectsStore()
/** Shared macros: the projects the user administers; personal ones: any project they work in. */
const projectChoices = computed(() => (props.shared ? cp.options.value : cp.all.value))
const { run } = useAction()
const items = ref<S['Macro'][]>([])
const loading = ref(true)
const failed = ref(false)
const dialog = ref(false)
const editing = ref<S['Macro'] | null>(null)

const sorted = computed(() => [...items.value].sort((a, b) => Number(a.shared) - Number(b.shared) || a.name.localeCompare(b.name)))
const editable = (m: S['Macro']) => (props.shared ? m.shared : !m.shared)
const tagNames = computed(() => lookups.tags.map((tg) => tg.name))

async function load() {
  loading.value = true
  failed.value = false
  try {
    items.value = (await unwrap(props.shared ? api.GET('/admin/macros') : api.GET('/agent/macros'))) ?? []
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(() => {
  void load()
  void lookups.ensure('staff', 'tags')
})

function openForm(m: S['Macro'] | null) {
  editing.value = m
  dialog.value = true
}
function onSaved() {
  dialog.value = false
  void load()
}
async function remove(m: S['Macro']) {
  if (!(await confirm({ body: t('agent.common.confirmDelete', { name: m.name }), color: 'error' }))) return
  const ok = await run(async () => {
    if (props.shared) await unwrap(api.DELETE('/admin/macros/{id}', { params: { path: { id: m.id } } }))
    else await unwrap(api.DELETE('/agent/macros/{id}', { params: { path: { id: m.id } } }))
    return true
  }, t('agent.common.deleted'))
  if (ok) void load()
}

/** A one-line summary of what the macro does. */
function effects(m: S['Macro']): string {
  const out: string[] = []
  if (m.reply_md.trim()) out.push(t(`automation.macros.kinds.${m.reply_kind}`))
  if (m.status) out.push(`${t('automation.macros.status')}: ${t(`status.${m.status}`)}`)
  if (m.priority) out.push(`${t('automation.macros.priority')}: ${t(`priority.${m.priority}`)}`)
  if (m.add_tags.length) out.push(`+${m.add_tags.join(', +')}`)
  if (m.remove_tags.length) out.push(`−${m.remove_tags.join(', −')}`)
  if (m.assignee === 'me') out.push(t('automation.macros.assignMe'))
  else if (m.assignee === 'none') out.push(t('automation.macros.unassign'))
  else if (m.assignee) out.push(`${t('automation.macros.assignee')}: ${lookups.staff.find((s) => s.id === m.assignee)?.name ?? '—'}`)
  return out.join(' · ')
}
const scopeLabel = (m: S['Macro']) => (m.shared ? t('automation.macros.shared') : t('automation.macros.personal'))
/** What the project column shows (ProjectCell): the project key, or "all projects". */
const projectKey = (id?: string | null) => (id ? (projects.byId(id)?.key ?? '?') : t('projects.config.all'))
const cols = useTableColumns('automation-macros', () => [
  { key: 'name', label: t('automation.macros.name'), locked: true },
  { key: 'does', label: t('automation.macros.does'), sortValue: false },
  { key: 'project', label: t('projects.config.project'), lowPriority: true, sortValue: (m: S['Macro']) => projectKey(m.project_id) },
  { key: 'scope', label: t('automation.macros.scope'), lowPriority: true, sortValue: scopeLabel },
  { key: 'updatedAt', label: t('agent.common.updatedAt'), lowPriority: true, sortValue: (m: S['Macro']) => m.updated_at },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader
      :title="shared ? $t('automation.nav.sharedMacros') : $t('automation.nav.myMacros')"
      :subtitle="shared ? $t('automation.macros.sharedSubtitle') : $t('automation.macros.subtitle')"
    >
      <template #actions>
        <v-btn color="primary" :prepend-icon="mdiPlus" data-testid="macro-new" @click="openForm(null)">{{ $t('automation.macros.new') }}</v-btn>
      </template>
    </PageHeader>
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="loading" indeterminate />
      <EmptyState v-if="!loading && !sorted.length" :title="$t('automation.macros.empty')" body="" />
      <template v-else>
      <ColumnsMenu :cols="cols" />
      <DataTable :cols="cols" :items="sorted" :loading="loading" actions density="comfortable" :row-props="{ 'data-testid': 'macro-row' }" data-testid="macros-table">
        <template #[`item.name`]="{ item }">{{ item.name }}</template>
        <template #[`item.does`]="{ item }"><span class="text-medium-emphasis">{{ effects(item) }}</span></template>
        <template #[`item.project`]="{ item }"><ProjectCell :id="item.project_id" /></template>
        <template #[`item.scope`]="{ item }"><v-chip size="small" label>{{ scopeLabel(item) }}</v-chip></template>
        <template #[`item.updatedAt`]="{ item }"><RelativeTime :value="item.updated_at" /></template>
        <template #[`item.actions`]="{ item }">
          <span v-if="editable(item)" class="text-no-wrap">
            <v-btn :icon="mdiPencil" variant="text" size="small" :aria-label="$t('agent.common.edit')" data-testid="macro-edit" @click="openForm(item)" />
            <v-btn :icon="mdiDelete" variant="text" size="small" :aria-label="$t('agent.common.delete')" data-testid="macro-delete" @click="remove(item)" />
          </span>
        </template>
      </DataTable>
      </template>
    </v-card>

    <v-dialog v-model="dialog" max-width="760" scrollable>
      <v-card :title="editing ? $t('automation.macros.edit') : $t('automation.macros.new')">
        <v-card-text>
          <MacroForm v-if="dialog" :macro="editing" :shared="shared" :staff="lookups.staff" :tags="tagNames" :projects="projectChoices" :can-global="!shared || cp.canGlobal.value" @saved="onSaved" @cancel="dialog = false" />
        </v-card-text>
      </v-card>
    </v-dialog>
  </v-container>
</template>
