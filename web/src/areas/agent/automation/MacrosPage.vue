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

type S = components['schemas']
const props = defineProps<{ shared?: boolean }>()
const { t } = useI18n()
const { confirm } = useConfirm()
const lookups = useLookupsStore()
const cp = useConfigProjects()
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
const cols = useTableColumns('automation-macros', () => [
  { key: 'name', label: t('automation.macros.name'), locked: true },
  { key: 'does', label: t('automation.macros.does') },
  { key: 'project', label: t('projects.config.project'), lowPriority: true },
  { key: 'scope', label: t('automation.macros.scope'), lowPriority: true },
  { key: 'updatedAt', label: t('agent.common.updatedAt'), lowPriority: true },
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
      <v-table density="comfortable" data-testid="macros-table">
        <thead>
          <tr>
            <th v-if="cols.show('name')">{{ $t('automation.macros.name') }}</th>
            <th v-if="cols.show('does')">{{ $t('automation.macros.does') }}</th>
            <th v-if="cols.show('project')">{{ $t('projects.config.project') }}</th>
            <th v-if="cols.show('scope')">{{ $t('automation.macros.scope') }}</th>
            <th v-if="cols.show('updatedAt')">{{ $t('agent.common.updatedAt') }}</th>
            <th class="text-right">{{ $t('agent.common.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="m in sorted" :key="m.id" data-testid="macro-row">
            <td v-if="cols.show('name')">{{ m.name }}</td>
            <td v-if="cols.show('does')" class="text-medium-emphasis">{{ effects(m) }}</td>
            <td v-if="cols.show('project')"><ProjectCell :id="m.project_id" /></td>
            <td v-if="cols.show('scope')">
              <v-chip size="small" label>{{ m.shared ? $t('automation.macros.shared') : $t('automation.macros.personal') }}</v-chip>
            </td>
            <td v-if="cols.show('updatedAt')"><RelativeTime :value="m.updated_at" /></td>
            <td class="text-right text-no-wrap">
              <template v-if="editable(m)">
                <v-btn :icon="mdiPencil" variant="text" size="small" :aria-label="$t('agent.common.edit')" data-testid="macro-edit" @click="openForm(m)" />
                <v-btn :icon="mdiDelete" variant="text" size="small" :aria-label="$t('agent.common.delete')" data-testid="macro-delete" @click="remove(m)" />
              </template>
            </td>
          </tr>
        </tbody>
      </v-table>
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
