<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/**
 * /agent/admin/inbound/log (FE-057, admin): every inbound message with its
 * outcome. Failed and quarantined messages can be retried after a confirm.
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiArrowLeft, mdiReplay } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { api, unwrap } from '@/api/client'
import { useConfirm } from '@/composables/useConfirm'
import { useCursorList } from '../shared/useCursorList'
import { useAction } from '../shared/useAction'
import type { components, operations } from '@/api/schema'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import DataTable from '@/components/DataTable.vue'

type S = components['schemas']
const { t } = useI18n()
const { confirm } = useConfirm()
const { run } = useAction()
const status = ref<S['InboundStatus'] | null>(null)
const mailboxId = ref<string | null>(null)
const mailboxes = ref<S['InboundMailbox'][]>([])
type InboundQuery = NonNullable<operations['listInboundMessages']['parameters']['query']>
const sort = ref<NonNullable<InboundQuery['sort']> | null>(null)

const STATUSES: S['InboundStatus'][] = ['processed', 'quarantined', 'failed', 'ignored']
const COLORS: Record<S['InboundStatus'], string> = { processed: 'success', quarantined: 'warning', failed: 'error', ignored: 'grey' }
const statusItems = computed(() => STATUSES.map((s) => ({ value: s, title: t(`agent.inboundLog.status.${s}`) })))
const mailboxName = (id: string) => mailboxes.value.find((m) => m.id === id)?.name ?? ''

const list = useCursorList<S['InboundMessage']>((cursor) =>
  unwrap(
    api.GET('/admin/inbound/messages', {
      params: { query: { cursor: cursor ?? undefined, sort: sort.value ?? undefined, status: status.value ?? undefined, mailbox_id: mailboxId.value ?? undefined } },
    }),
  ),
)
watch([status, mailboxId, sort], () => void list.reload())
onMounted(async () => {
  void list.reload()
  try {
    mailboxes.value = (await unwrap(api.GET('/admin/inbound/mailboxes'))) ?? []
  } catch {
    /* the filter just stays empty */
  }
})

const retryable = (m: S['InboundMessage']) => m.status === 'failed' || m.status === 'quarantined'

async function retry(m: S['InboundMessage']) {
  if (!(await confirm({ body: t('agent.inboundLog.retryConfirm', { subject: m.subject || m.from_addr }), confirmText: t('agent.inboundLog.retry') }))) return
  const updated = await run(
    () => unwrap(api.POST('/admin/inbound/messages/{id}/retry', { params: { path: { id: m.id } } })),
    t('agent.inboundLog.retried'),
  )
  if (updated) list.items.value = list.items.value.map((x) => (x.id === m.id ? updated : x))
}

const cols = useTableColumns('inbound-log', () => [
  { key: 'received', label: t('agent.inboundLog.received'), lowPriority: true, sort: { asc: 'received_at', desc: '-received_at' } },
  { key: 'sender', label: t('agent.inboundLog.sender') },
  { key: 'subject', label: t('agent.inboundLog.subject'), locked: true },
  { key: 'status', label: t('agent.common.status') },
  { key: 'reason', label: t('agent.inboundLog.reason'), lowPriority: true },
  { key: 'ticket', label: t('agent.inboundLog.ticket'), lowPriority: true },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('agent.admin.nav.inboundLog')" :subtitle="$t('agent.inboundLog.subtitle')">
      <template #actions>
        <v-btn variant="text" :prepend-icon="mdiArrowLeft" to="/agent/admin/inbound">{{ $t('agent.admin.nav.inbound') }}</v-btn>
      </template>
    </PageHeader>
    <div class="d-flex flex-wrap ga-3 mb-3">
      <AutocompleteSearch v-model="status" :items="statusItems" :label="$t('agent.common.status')" density="compact" hide-details clearable style="max-width: 220px" data-testid="inlog-status" />
      <AutocompleteSearch v-model="mailboxId" :items="mailboxes" item-title="name" item-value="id" :label="$t('agent.inboundLog.mailbox')" density="compact" hide-details clearable style="max-width: 260px" />
    </div>
    <v-alert v-if="list.error.value" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="list.loading.value" indeterminate />
      <EmptyState v-if="!list.loading.value && !list.items.value.length" :title="$t('agent.inboundLog.empty')" body="" />
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
          :row-props="{ 'data-testid': 'inlog-row' }"
          data-testid="inlog-table"
          @update:sort="(v: string | null) => (sort = v as typeof sort)"
          @load-more="list.loadMore()"
        >
          <template #[`item.received`]="{ item }"><span class="text-no-wrap"><RelativeTime :value="item.received_at" /></span></template>
          <template #[`item.sender`]="{ item }">{{ item.from_addr }}<div v-if="mailboxName(item.mailbox_id)" class="text-caption text-medium-emphasis">{{ mailboxName(item.mailbox_id) }}</div></template>
          <template #[`item.subject`]="{ item }">{{ item.subject || '—' }}</template>
          <template #[`item.status`]="{ item }"><v-chip size="small" label variant="tonal" :color="COLORS[item.status as S['InboundStatus']]" data-testid="inlog-status-chip">{{ $t(`agent.inboundLog.status.${item.status}`) }}</v-chip></template>
          <template #[`item.reason`]="{ item }"><span class="text-caption">{{ item.reason || '—' }}</span></template>
          <template #[`item.ticket`]="{ item }">
            <router-link v-if="item.ticket_id" :to="{ path: '/agent/tickets', query: { q: item.ticket_id } }" data-testid="inlog-ticket">{{ $t('agent.inboundLog.openTicket') }}</router-link>
            <span v-else>—</span>
          </template>
          <template #[`item.actions`]="{ item }">
            <v-btn v-if="retryable(item)" variant="text" size="small" :prepend-icon="mdiReplay" data-testid="inlog-retry" @click="retry(item)">{{ $t('agent.inboundLog.retry') }}</v-btn>
          </template>
        </DataTable>
      </template>
    </v-card>
  </v-container>
</template>
