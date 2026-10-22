<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/admin/automation (FE-062, supervisor+, D-19 A): trigger and time
 * rules in execution order. Enable/disable inline, move up/down (one
 * `POST …/reorder` with every id), delete after confirmation, the builder
 * (FE-063) in a dialog and a dry-run per saved rule.
 */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiArrowDown, mdiArrowUp, mdiDelete, mdiFlaskOutline, mdiLightningBolt, mdiPencil, mdiPlus, mdiStopCircleOutline } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import RuleForm from './RuleForm.vue'
import DryRunForm from './DryRunForm.vue'
import { api, unwrap } from '@/api/client'
import { useConfirm } from '@/composables/useConfirm'
import { useLookupsStore } from '../shared/lookups'
import ProjectCell from '../shared/ProjectCell.vue'
import { useConfigProjects } from '../shared/useConfigProjects'
import { useAction } from '../shared/useAction'
import type { components } from '@/api/schema'
import SectionIcon from '@/components/SectionIcon.vue'
import { useProjectsStore } from '@/stores/projects'
import DataTable from '@/components/DataTable.vue'

type S = components['schemas']
const { t } = useI18n()
const { confirm } = useConfirm()
const lookups = useLookupsStore()
const cp = useConfigProjects()
const projects = useProjectsStore()
const { run, busy } = useAction()
const rules = ref<S['AutomationRule'][]>([])
const loading = ref(true)
const failed = ref(false)
const dialog = ref(false)
const editing = ref<S['AutomationRule'] | null>(null)
const testing = ref<S['AutomationRule'] | null>(null)
const canned = ref<S['CannedResponse'][]>([])
const organizations = ref<S['Organization'][]>([])
let refsLoaded = false

async function load() {
  loading.value = true
  failed.value = false
  try {
    rules.value = ((await unwrap(api.GET('/admin/automation/rules'))) ?? []).sort((a, b) => a.position - b.position)
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(() => void load())

/** Reference data for the builder, loaded once when it first opens. */
async function loadRefs() {
  if (refsLoaded) return
  refsLoaded = true
  await Promise.all([
    lookups.ensure('teams', 'categories', 'staff', 'customFields'),
    unwrap(api.GET('/admin/canned-responses'))
      .then((c) => (canned.value = c ?? []))
      .catch(() => {}),
    unwrap(api.GET('/agent/organizations', { params: { query: { limit: 200 } } }))
      .then((p) => (organizations.value = p.items ?? []))
      .catch(() => {}),
  ])
}

async function openForm(r: S['AutomationRule'] | null) {
  editing.value = r
  await loadRefs()
  dialog.value = true
}
function onSaved() {
  dialog.value = false
  void load()
}

async function toggle(r: S['AutomationRule'], enabled: boolean) {
  const saved = await run(() => unwrap(api.PATCH('/admin/automation/rules/{id}', { params: { path: { id: r.id } }, body: { enabled } })))
  if (saved) rules.value = rules.value.map((x) => (x.id === saved.id ? saved : x))
}

async function move(i: number, dir: -1 | 1) {
  const ids = rules.value.map((r) => r.id)
  const j = i + dir
  if (j < 0 || j >= ids.length) return
  ;[ids[i], ids[j]] = [ids[j]!, ids[i]!]
  const out = await run(() => unwrap(api.POST('/admin/automation/rules/reorder', { body: { ids } })))
  if (out) rules.value = [...out].sort((a, b) => a.position - b.position)
}

async function remove(r: S['AutomationRule']) {
  if (!(await confirm({ body: t('agent.common.confirmDelete', { name: r.name }), color: 'error' }))) return
  const ok = await run(async () => {
    await unwrap(api.DELETE('/admin/automation/rules/{id}', { params: { path: { id: r.id } } }))
    return true
  }, t('agent.common.deleted'))
  if (ok) void load()
}
/** Execution index of a rule; the table may be sorted by another column. */
const at = (r: S['AutomationRule']) => rules.value.findIndex((x) => x.id === r.id)
const eventLabel = (r: S['AutomationRule']) => t(`automation.events.${r.event.replace('.', '_')}`)
/** What the project column shows (ProjectCell): the project key, or "all projects". */
const projectKey = (id?: string | null) => (id ? (projects.byId(id)?.key ?? '?') : t('projects.config.all'))
const cols = useTableColumns('automation-rules', () => [
  { key: 'position', label: '#', locked: true, sortValue: at },
  { key: 'name', label: t('automation.rules.name'), locked: true },
  { key: 'project', label: t('projects.config.project'), lowPriority: true, sortValue: (r: S['AutomationRule']) => projectKey(r.project_id) },
  { key: 'event', label: t('automation.rules.event'), sortValue: eventLabel },
  { key: 'summary', label: t('automation.rules.summary'), lowPriority: true, sortValue: false },
  { key: 'enabled', label: t('automation.rules.enabled') },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('automation.nav.rules')" :subtitle="$t('automation.rules.subtitle')">
      <template #actions>
        <v-btn color="primary" :prepend-icon="mdiPlus" data-testid="rule-new" @click="openForm(null)">{{ $t('automation.rules.new') }}</v-btn>
      </template>
    </PageHeader>
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="loading" indeterminate />
      <EmptyState v-if="!loading && !rules.length" :title="$t('automation.rules.empty')" :body="$t('automation.rules.emptyBody')" />
      <template v-else>
      <ColumnsMenu :cols="cols" />
      <DataTable :cols="cols" :items="rules" :loading="loading" actions density="comfortable" :row-props="{ 'data-testid': 'rule-row' }" data-testid="rules-table">
        <template #[`item.position`]="{ item }">{{ at(item) + 1 }}</template>
        <template #[`item.name`]="{ item }">
          <SectionIcon section="automation" :icon="mdiLightningBolt" :size="26" class="mr-2 align-middle" />{{ item.name }}
          <v-icon v-if="item.stop_processing" :icon="mdiStopCircleOutline" size="small" class="ml-1" :title="$t('automation.rules.stopProcessing')" data-testid="rule-stop-icon" />
        </template>
        <template #[`item.project`]="{ item }"><ProjectCell :id="item.project_id" /></template>
        <template #[`item.event`]="{ item }">{{ eventLabel(item) }}</template>
        <template #[`item.summary`]="{ item }">
          <span class="text-medium-emphasis">{{ $t('automation.rules.counts', { conditions: item.conditions.length, actions: item.actions.length }) }}</span>
        </template>
        <template #[`item.enabled`]="{ item }">
          <v-switch
            :model-value="item.enabled"
            color="primary"
            density="compact"
            hide-details
            :disabled="busy"
            :aria-label="$t('automation.rules.enabled')"
            data-testid="rule-toggle"
            @update:model-value="(v) => toggle(item, !!v)"
          />
        </template>
        <template #[`item.actions`]="{ item }">
          <span class="text-no-wrap">
            <v-btn :icon="mdiArrowUp" variant="text" size="small" :disabled="at(item) === 0 || busy" :aria-label="$t('automation.rules.moveUp')" data-testid="rule-up" @click="move(at(item), -1)" />
            <v-btn
              :icon="mdiArrowDown"
              variant="text"
              size="small"
              :disabled="at(item) === rules.length - 1 || busy"
              :aria-label="$t('automation.rules.moveDown')"
              data-testid="rule-down"
              @click="move(at(item), 1)"
            />
            <v-btn :icon="mdiFlaskOutline" variant="text" size="small" :aria-label="$t('automation.dryRun.title')" data-testid="rule-test" @click="testing = item" />
            <v-btn :icon="mdiPencil" variant="text" size="small" :aria-label="$t('agent.common.edit')" data-testid="rule-edit" @click="openForm(item)" />
            <v-btn :icon="mdiDelete" variant="text" size="small" :aria-label="$t('agent.common.delete')" data-testid="rule-delete" @click="remove(item)" />
          </span>
        </template>
      </DataTable>
      </template>
    </v-card>

    <v-dialog v-model="dialog" max-width="1100" scrollable>
      <v-card :title="editing ? $t('automation.rules.edit') : $t('automation.rules.new')">
        <v-card-text>
          <RuleForm
            v-if="dialog"
            :rule="editing"
            :teams="lookups.teams"
            :categories="lookups.categories"
            :staff="lookups.staff"
            :custom-fields="lookups.customFields"
            :canned="canned"
            :organizations="organizations"
            :projects="cp.options.value"
            :can-global="cp.canGlobal.value"
            @saved="onSaved"
            @cancel="dialog = false"
          />
        </v-card-text>
      </v-card>
    </v-dialog>
    <v-dialog :model-value="!!testing" max-width="640" @update:model-value="(v: boolean) => !v && (testing = null)">
      <v-card v-if="testing" :title="`${$t('automation.dryRun.title')}: ${testing.name}`">
        <v-card-text><DryRunForm :rule="testing" @close="testing = null" /></v-card-text>
      </v-card>
    </v-dialog>
  </v-container>
</template>
