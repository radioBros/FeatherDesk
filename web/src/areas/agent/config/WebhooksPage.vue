<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/admin/webhooks (FE-040, admin): list, create/edit, secret shown
 * once (rendered from a local ref that is cleared on close), rotate secret,
 * test, deliveries log, re-enable after auto-disable, delete.
 */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiDelete, mdiHistory, mdiKeyChange, mdiPencil, mdiPlus } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import WebhookForm from './WebhookForm.vue'
import WebhookDeliveries from './WebhookDeliveries.vue'
import SecretOnce from '../shared/SecretOnce.vue'
import { api, unwrap } from '@/api/client'
import { useConfirm } from '@/composables/useConfirm'
import { useUiStore } from '@/stores/ui'
import { useAction } from '../shared/useAction'
import ProjectCell from '../shared/ProjectCell.vue'
import { useConfigProjects } from '../shared/useConfigProjects'
import type { components } from '@/api/schema'
import ColumnCells from '@/components/ColumnCells.vue'

type S = components['schemas']
const { t } = useI18n()
const { confirm } = useConfirm()
const ui = useUiStore()
const { run } = useAction()
const cp = useConfigProjects()

const items = ref<S['Webhook'][]>([])
const loading = ref(true)
const failed = ref(false)
const dialog = ref(false)
const editing = ref<S['Webhook'] | null>(null)
const secret = ref<string | null>(null)
const deliveriesFor = ref<S['Webhook'] | null>(null)

async function load() {
  loading.value = true
  failed.value = false
  try {
    items.value = (await unwrap(api.GET('/admin/webhooks'))) ?? []
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(load)

function openForm(w: S['Webhook'] | null) {
  editing.value = w
  dialog.value = true
}
function onSaved(_w: S['Webhook'], newSecret: string | null) {
  dialog.value = false
  if (newSecret) secret.value = newSecret
  ui.notify(t('agent.common.saved'), 'success')
  void load()
}
function closeSecret() {
  secret.value = null
}

async function rotate(w: S['Webhook']) {
  if (!(await confirm({ body: t('agent.webhooks.rotateConfirm'), color: 'warning' }))) return
  const res = await run(() => unwrap(api.PATCH('/admin/webhooks/{id}', { params: { path: { id: w.id } }, body: { rotate_secret: true } })))
  if (!res) return
  const s = (res as S['Webhook'] & { secret?: string }).secret
  if (s) secret.value = s
  else ui.notify(t('agent.webhooks.rotated'), 'success')
  void load()
}

async function reenable(w: S['Webhook']) {
  const res = await run(
    () => unwrap(api.PATCH('/admin/webhooks/{id}', { params: { path: { id: w.id } }, body: { active: true } })),
    t('agent.webhooks.reenabled'),
  )
  if (res) void load()
}

async function remove(w: S['Webhook']) {
  if (!(await confirm({ body: t('agent.common.confirmDelete', { name: w.url }), color: 'error' }))) return
  const ok = await run(async () => {
    await unwrap(api.DELETE('/admin/webhooks/{id}', { params: { path: { id: w.id } } }))
    return true
  }, t('agent.common.deleted'))
  if (ok) void load()
}
const cols = useTableColumns('config-webhooks', () => [
  { key: 'url', label: t('agent.webhooks.url'), locked: true },
  { key: 'projects', label: t('projects.config.projects'), lowPriority: true },
  { key: 'events', label: t('agent.webhooks.events'), lowPriority: true },
  { key: 'status', label: t('agent.common.status') },
  { key: 'failures', label: t('agent.webhooks.failures'), lowPriority: true },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('agent.admin.nav.webhooks')" :subtitle="$t('agent.webhooks.subtitle')">
      <template #actions>
        <v-btn color="primary" :prepend-icon="mdiPlus" data-testid="webhook-new" @click="openForm(null)">{{ $t('agent.webhooks.new') }}</v-btn>
      </template>
    </PageHeader>
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="loading" indeterminate />
      <EmptyState v-if="!loading && !items.length" :title="$t('agent.webhooks.empty')" body="" />
      <template v-else>
      <ColumnsMenu :cols="cols" />
      <v-table density="comfortable" data-testid="webhook-table">
        <thead>
          <tr>
            <ColumnCells :cols="cols" head>
              <th data-col="url" v-if="cols.show('url')">{{ $t('agent.webhooks.url') }}</th>
              <th data-col="projects" v-if="cols.show('projects')">{{ $t('projects.config.projects') }}</th>
              <th data-col="events" v-if="cols.show('events')">{{ $t('agent.webhooks.events') }}</th>
              <th data-col="status" v-if="cols.show('status')">{{ $t('agent.common.status') }}</th>
              <th data-col="failures" v-if="cols.show('failures')">{{ $t('agent.webhooks.failures') }}</th>
              <th class="text-right">{{ $t('agent.common.actions') }}</th>
            </ColumnCells>
          </tr>
        </thead>
        <tbody>
          <tr v-for="w in items" :key="w.id" data-testid="webhook-row">
            <ColumnCells :cols="cols">
              <td data-col="url" v-if="cols.show('url')" class="text-truncate" style="max-width: 320px">
                {{ w.url }}
                <v-chip v-if="w.include_internal" size="x-small" label color="warning" class="ml-1">{{ $t('agent.webhooks.internalChip') }}</v-chip>
              </td>
              <td data-col="projects" v-if="cols.show('projects')"><ProjectCell :ids="w.project_ids ?? []" /></td>
              <td data-col="events" v-if="cols.show('events')">{{ $t('agent.webhooks.eventCount', { n: w.events.length }) }}</td>
              <td data-col="status" v-if="cols.show('status')">
                <v-chip v-if="w.active" size="small" label color="success">{{ $t('agent.common.active') }}</v-chip>
                <template v-else>
                  <v-chip size="small" label color="error">{{ $t('agent.common.disabled') }}</v-chip>
                  <div v-if="w.disabled_reason" class="text-caption text-medium-emphasis" data-testid="webhook-disabled-reason">{{ w.disabled_reason }}</div>
                </template>
              </td>
              <td data-col="failures" v-if="cols.show('failures')">{{ w.consecutive_failures ?? 0 }}</td>
              <td class="text-right text-no-wrap">
                <v-btn v-if="!w.active" size="small" variant="tonal" color="primary" class="mr-1" data-testid="webhook-reenable" @click="reenable(w)">
                  {{ $t('agent.webhooks.reenable') }}
                </v-btn>
                <v-btn :icon="mdiHistory" variant="text" size="small" :aria-label="$t('agent.webhooks.deliveries')" data-testid="webhook-deliveries-btn" @click="deliveriesFor = w" />
                <v-btn :icon="mdiKeyChange" variant="text" size="small" :aria-label="$t('agent.webhooks.rotate')" data-testid="webhook-rotate" @click="rotate(w)" />
                <v-btn :icon="mdiPencil" variant="text" size="small" :aria-label="$t('agent.common.edit')" data-testid="webhook-edit" @click="openForm(w)" />
                <v-btn :icon="mdiDelete" variant="text" size="small" :aria-label="$t('agent.common.delete')" data-testid="webhook-delete" @click="remove(w)" />
              </td>
            </ColumnCells>
          </tr>
        </tbody>
      </v-table>
      </template>
    </v-card>

    <v-dialog v-model="dialog" max-width="640">
      <v-card :title="editing ? $t('agent.webhooks.edit') : $t('agent.webhooks.new')">
        <v-card-text>
          <WebhookForm v-if="dialog" :webhook="editing" :projects="cp.all.value" @saved="onSaved" @cancel="dialog = false" />
        </v-card-text>
      </v-card>
    </v-dialog>

    <v-dialog :model-value="!!secret" max-width="560" persistent>
      <v-card :title="$t('agent.webhooks.secretTitle')" data-testid="webhook-secret-dialog">
        <v-card-text>
          <SecretOnce v-if="secret" :value="secret" :label="$t('agent.webhooks.secret')" />
          <p class="text-body-2 mt-2">{{ $t('agent.webhooks.secretHint') }}</p>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn color="primary" variant="flat" data-testid="webhook-secret-close" @click="closeSecret">{{ $t('agent.common.done') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog :model-value="!!deliveriesFor" max-width="900" @update:model-value="(v: boolean) => !v && (deliveriesFor = null)">
      <v-card :title="$t('agent.webhooks.deliveries')" :subtitle="deliveriesFor?.url">
        <v-card-text>
          <WebhookDeliveries v-if="deliveriesFor" :webhook-id="deliveriesFor.id" />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="deliveriesFor = null">{{ $t('app.close') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </v-container>
</template>
