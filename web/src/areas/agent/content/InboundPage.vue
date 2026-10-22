<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/admin/inbound (FE-057, admin): inbound mailboxes. A webhook
 * mailbox's ingest URL (with its token) is shown exactly once after creation
 * through `SecretOnce`, then dropped from memory.
 */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiDeleteOutline, mdiPencil, mdiPlus, mdiRefresh, mdiTextBoxSearchOutline } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import SecretOnce from '../shared/SecretOnce.vue'
import InboundMailboxForm from './InboundMailboxForm.vue'
import { api, unwrap } from '@/api/client'
import { useConfirm } from '@/composables/useConfirm'
import { useLookupsStore } from '../shared/lookups'
import ProjectCell from '../shared/ProjectCell.vue'
import { useConfigProjects } from '../shared/useConfigProjects'
import { useAction } from '../shared/useAction'
import type { components } from '@/api/schema'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import DataTable from '@/components/DataTable.vue'
import { useProjectsStore } from '@/stores/projects'

type Mailbox = components['schemas']['InboundMailbox']
const { t } = useI18n()
const { confirm } = useConfirm()
const { run } = useAction()
const lookups = useLookupsStore()
const cp = useConfigProjects()
const projects = useProjectsStore()
const items = ref<Mailbox[]>([])
const loading = ref(true)
const failed = ref(false)
const dialog = ref(false)
const editing = ref<Mailbox | null>(null)
/** The one-time ingest URL; nulled when its dialog closes. */
const secretUrl = ref<string | null>(null)

async function load() {
  loading.value = true
  failed.value = false
  try {
    // The list never carries webhook_url (it is returned once on creation).
    items.value = ((await unwrap(api.GET('/admin/inbound/mailboxes'))) ?? []).map((m) => ({ ...m, webhook_url: undefined }))
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

function openForm(m: Mailbox | null) {
  editing.value = m
  dialog.value = true
}
function onSaved(m: Mailbox, created: boolean) {
  dialog.value = false
  if (created && m.kind === 'webhook' && m.webhook_url) secretUrl.value = m.webhook_url
  void load()
}
function closeSecret() {
  secretUrl.value = null
}

async function remove(m: Mailbox) {
  if (!(await confirm({ body: t('agent.common.confirmDelete', { name: m.name }), color: 'error', confirmText: t('agent.common.delete') }))) return
  // DELETE answers 204 with no body: return a marker so success is distinguishable from failure.
  const ok = await run(async () => (await unwrap(api.DELETE('/admin/inbound/mailboxes/{id}', { params: { path: { id: m.id } } })), true), t('agent.common.deleted'))
  if (ok !== undefined) await load()
}

const statusLabel = (m: Mailbox) => (m.last_error ? t('agent.inbound.error') : m.active ? t('agent.common.active') : t('agent.common.inactive'))
const teamLabel = (m: Mailbox) => lookups.teamName(m.team_id) || t('agent.inbound.triage')

const cols = useTableColumns('inbound', () => [
  { key: 'name', label: t('agent.common.name'), locked: true },
  { key: 'kind', label: t('agent.inbound.kindLabel'), lowPriority: true, sortValue: (m: Mailbox) => t(`agent.inbound.kind.${m.kind}`) },
  { key: 'address', label: t('agent.inbound.address'), lowPriority: true },
  { key: 'project', label: t('projects.config.project'), lowPriority: true, sortValue: (m: Mailbox) => (m.project_id ? projects.byId(m.project_id)?.key ?? '?' : '') },
  { key: 'team', label: t('agent.common.team'), lowPriority: true, sortValue: teamLabel },
  { key: 'status', label: t('agent.common.status'), sortValue: statusLabel },
  { key: 'lastOk', label: t('agent.inbound.lastOk'), lowPriority: true, sortValue: (m: Mailbox) => m.last_ok_at ?? '' },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('agent.admin.nav.inbound')" :subtitle="$t('agent.inbound.subtitle')">
      <template #actions>
        <v-btn variant="text" :prepend-icon="mdiTextBoxSearchOutline" to="/agent/admin/inbound/log">{{ $t('agent.admin.nav.inboundLog') }}</v-btn>
        <v-btn color="primary" :prepend-icon="mdiPlus" data-testid="inbound-new" @click="openForm(null)">{{ $t('agent.inbound.new') }}</v-btn>
      </template>
    </PageHeader>
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="loading" indeterminate />
      <EmptyState v-if="!loading && !items.length" :title="$t('agent.inbound.empty')" :body="$t('agent.inbound.emptyBody')" />
      <template v-else>
        <ColumnsMenu :cols="cols" />
        <DataTable :cols="cols" :items="items" :loading="loading" actions density="comfortable" :row-props="{ 'data-testid': 'inbound-row' }" data-testid="inbound-table">
          <template #[`item.name`]="{ item }">{{ item.name }}</template>
          <template #[`item.kind`]="{ item }">{{ $t(`agent.inbound.kind.${item.kind}`) }}</template>
          <template #[`item.address`]="{ item }">{{ item.address }}</template>
          <template #[`item.project`]="{ item }"><ProjectCell :id="item.project_id" /></template>
          <template #[`item.team`]="{ item }">{{ teamLabel(item) }}</template>
          <template #[`item.status`]="{ item }">
            <v-chip size="small" label variant="tonal" :color="item.last_error ? 'error' : item.active ? 'success' : undefined">{{ statusLabel(item) }}</v-chip>
            <div v-if="item.last_error" class="text-caption text-error mt-1" data-testid="inbound-last-error">{{ item.last_error }}</div>
          </template>
          <template #[`item.lastOk`]="{ item }"><RelativeTime v-if="item.last_ok_at" :value="item.last_ok_at" /><span v-else>{{ $t('agent.common.never') }}</span></template>
          <template #[`item.actions`]="{ item }">
            <div class="text-no-wrap">
              <v-tooltip v-if="item.kind === 'webhook'" :text="$t('agent.inbound.regenerateHint')" location="top">
                <template #activator="{ props: tip }">
                  <span v-bind="tip"><v-btn :icon="mdiRefresh" variant="text" size="small" disabled :aria-label="$t('agent.inbound.regenerate')" data-testid="inbound-regenerate" /></span>
                </template>
              </v-tooltip>
              <v-btn :icon="mdiPencil" variant="text" size="small" :aria-label="$t('agent.common.edit')" data-testid="inbound-edit" @click="openForm(item)" />
              <v-btn :icon="mdiDeleteOutline" variant="text" size="small" color="error" :aria-label="$t('agent.common.delete')" data-testid="inbound-delete" @click="remove(item)" />
            </div>
          </template>
        </DataTable>
      </template>
    </v-card>

    <v-dialog v-model="dialog" max-width="720">
      <v-card :title="editing ? $t('agent.inbound.edit') : $t('agent.inbound.new')">
        <v-card-text>
          <InboundMailboxForm v-if="dialog" :mailbox="editing" :teams="lookups.teams" :projects="cp.options.value" @saved="onSaved" @cancel="dialog = false" />
        </v-card-text>
      </v-card>
    </v-dialog>

    <v-dialog :model-value="!!secretUrl" max-width="640" persistent>
      <v-card :title="$t('agent.inbound.urlTitle')">
        <v-card-text>
          <SecretOnce v-if="secretUrl" :value="secretUrl" :label="$t('agent.inbound.url')" :warning="$t('agent.inbound.urlWarning')" />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn color="primary" variant="flat" data-testid="secret-close" @click="closeSecret">{{ $t('agent.common.done') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </v-container>
</template>
