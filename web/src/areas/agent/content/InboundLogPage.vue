<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
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
import type { components } from '@/api/schema'

type S = components['schemas']
const { t } = useI18n()
const { confirm } = useConfirm()
const { run } = useAction()
const status = ref<S['InboundStatus'] | null>(null)
const mailboxId = ref<string | null>(null)
const mailboxes = ref<S['InboundMailbox'][]>([])

const STATUSES: S['InboundStatus'][] = ['processed', 'quarantined', 'failed', 'ignored']
const COLORS: Record<S['InboundStatus'], string> = { processed: 'success', quarantined: 'warning', failed: 'error', ignored: 'grey' }
const statusItems = computed(() => STATUSES.map((s) => ({ value: s, title: t(`agent.inboundLog.status.${s}`) })))
const mailboxName = (id: string) => mailboxes.value.find((m) => m.id === id)?.name ?? ''

const list = useCursorList<S['InboundMessage']>((cursor) =>
  unwrap(
    api.GET('/admin/inbound/messages', {
      params: { query: { cursor: cursor ?? undefined, status: status.value ?? undefined, mailbox_id: mailboxId.value ?? undefined } },
    }),
  ),
)
watch([status, mailboxId], () => void list.reload())
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
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('agent.admin.nav.inboundLog')" :subtitle="$t('agent.inboundLog.subtitle')">
      <template #actions>
        <v-btn variant="text" :prepend-icon="mdiArrowLeft" to="/agent/admin/inbound">{{ $t('agent.admin.nav.inbound') }}</v-btn>
      </template>
    </PageHeader>
    <div class="d-flex flex-wrap ga-3 mb-3">
      <v-select v-model="status" :items="statusItems" :label="$t('agent.common.status')" density="compact" hide-details clearable style="max-width: 220px" data-testid="inlog-status" />
      <v-select v-model="mailboxId" :items="mailboxes" item-title="name" item-value="id" :label="$t('agent.inboundLog.mailbox')" density="compact" hide-details clearable style="max-width: 260px" />
    </div>
    <v-alert v-if="list.error.value" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="list.loading.value" indeterminate />
      <EmptyState v-if="!list.loading.value && !list.items.value.length" :title="$t('agent.inboundLog.empty')" body="" />
      <v-table v-else density="comfortable" data-testid="inlog-table">
        <thead>
          <tr>
            <th>{{ $t('agent.inboundLog.received') }}</th>
            <th>{{ $t('agent.inboundLog.sender') }}</th>
            <th>{{ $t('agent.inboundLog.subject') }}</th>
            <th>{{ $t('agent.common.status') }}</th>
            <th>{{ $t('agent.inboundLog.reason') }}</th>
            <th>{{ $t('agent.inboundLog.ticket') }}</th>
            <th class="text-right">{{ $t('agent.common.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="m in list.items.value" :key="m.id" data-testid="inlog-row">
            <td class="text-no-wrap"><RelativeTime :value="m.received_at" /></td>
            <td>{{ m.from_addr }}<div v-if="mailboxName(m.mailbox_id)" class="text-caption text-medium-emphasis">{{ mailboxName(m.mailbox_id) }}</div></td>
            <td>{{ m.subject || '—' }}</td>
            <td><v-chip size="small" label variant="tonal" :color="COLORS[m.status]" data-testid="inlog-status-chip">{{ $t(`agent.inboundLog.status.${m.status}`) }}</v-chip></td>
            <td class="text-caption">{{ m.reason || '—' }}</td>
            <td>
              <router-link v-if="m.ticket_id" :to="{ path: '/agent/tickets', query: { q: m.ticket_id } }" data-testid="inlog-ticket">{{ $t('agent.inboundLog.openTicket') }}</router-link>
              <span v-else>—</span>
            </td>
            <td class="text-right">
              <v-btn v-if="retryable(m)" variant="text" size="small" :prepend-icon="mdiReplay" data-testid="inlog-retry" @click="retry(m)">{{ $t('agent.inboundLog.retry') }}</v-btn>
            </td>
          </tr>
        </tbody>
      </v-table>
      <div v-if="list.next.value" class="pa-3 text-center">
        <v-btn variant="text" :loading="list.loading.value" @click="list.loadMore()">{{ $t('agent.common.loadMore') }}</v-btn>
      </div>
    </v-card>
  </v-container>
</template>
