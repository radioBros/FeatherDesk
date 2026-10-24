<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { mdiArrowLeft, mdiCheckCircleOutline, mdiRestore, mdiInformationOutline } from '@mdi/js'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { useRealtime } from '@/composables/useRealtime'
import { useConfirm } from '@/composables/useConfirm'
import { useSessionStore } from '@/stores/session'
import { useUiStore } from '@/stores/ui'
import type { ServerEvent } from '@/realtime/client'
import MessageThread from '@/components/MessageThread.vue'
import StatusChip from '@/components/StatusChip.vue'
import PriorityChip from '@/components/PriorityChip.vue'
import ProjectChip from '@/components/ProjectChip.vue'
import TicketNumber from '@/components/TicketNumber.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import EmptyState from '@/components/EmptyState.vue'
import ReplyForm from '../forms/ReplyForm.vue'
import CsatForm from '../forms/CsatForm.vue'
import CustomFieldValues from '../components/CustomFieldValues.vue'
import { toThreadMessage, type CustomFieldDef, type CustomerMessage, type CustomerTicket } from '../types'

/**
 * Ticket detail, public thread only (FE-017). Buttons exist iff their action is
 * in `allowed_actions` (permissions §6.4). Live over `ticket:<id>` (FE-044):
 * new public messages are appended from the frame without an HTTP request.
 */
const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const session = useSessionStore()
const ui = useUiStore()
const { confirm } = useConfirm()

// `/tickets/WEB-12` (by ref) or `/tickets/<id>`; sockets and actions use the loaded ticket's id.
const key = computed(() => String(route.params.ref ?? route.params.id ?? ''))
const byRef = computed(() => route.params.ref !== undefined)
const id = computed(() => ticket.value?.id ?? (byRef.value ? '' : key.value))
const ticket = ref<CustomerTicket | null>(null)
const defs = ref<CustomFieldDef[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const busy = ref<'resolve' | 'reopen' | null>(null)
const followUpId = ref<string | null>(null)
const rated = ref(false)

const thread = computed(() => (ticket.value?.messages ?? []).filter((m) => m.kind === 'public').map(toThreadMessage))
const can = (action: string) => session.can(action, ticket.value)
const closed = computed(() => ticket.value?.status === 'closed')

async function load() {
  error.value = null
  try {
    ticket.value = byRef.value
      ? await unwrap(api.GET('/portal/tickets/by-ref/{ref}', { params: { path: { ref: key.value } } }))
      : await unwrap(api.GET('/portal/tickets/{id}', { params: { path: { id: key.value } } }))
  } catch (e) {
    ticket.value = null
    error.value = e instanceof ApiProblem && e.status === 404 ? t('portal.detail.notFound') : e instanceof ApiProblem ? e.userMessage : t('errors.generic')
  } finally {
    loading.value = false
  }
}

async function loadFields() {
  try {
    defs.value = (await unwrap(api.GET('/portal/custom-fields'))) ?? []
  } catch {
    defs.value = []
  }
}

watch(
  key,
  () => {
    loading.value = true
    followUpId.value = null
    rated.value = false
    void load()
  },
  { immediate: true },
)
void loadFields()

function appendMessage(m: CustomerMessage) {
  if (!ticket.value || m.kind !== 'public') return
  if (ticket.value.messages.some((x) => x.id === m.id)) return
  ticket.value.messages.push(m)
}

function onEvent(ev: ServerEvent) {
  if (!ticket.value) return
  switch (ev.event) {
    case 'ticket.access_lost':
      // The global `me` handler already shows the toast; only leave the page.
      void router.replace('/tickets')
      return
    case 'message.created': {
      const m = ev.data?.message as CustomerMessage | undefined
      if (m) appendMessage(m)
      const tk = ev.data?.ticket as Partial<CustomerTicket> | undefined
      if (tk?.status && tk.status !== ticket.value.status) void load()
      return
    }
    case 'ticket.followup_created': {
      const nid = (ev.data?.new_ticket_id ?? (ev.data?.changes as Record<string, unknown> | undefined)?.new_ticket_id) as string | undefined
      if (nid && nid !== ticket.value.id) followUpId.value = nid
      return
    }
    default:
      // Status, fields, merge…: allowed_actions may change with them, so read the ticket once.
      if (ev.event.startsWith('ticket.')) void load()
  }
}
useRealtime(() => (id.value ? [`ticket:${id.value}`] : []), { onEvent, onResync: () => void load() })

async function act(kind: 'resolve' | 'reopen') {
  if (!ticket.value) return
  if (kind === 'resolve' && !(await confirm({ title: t('portal.detail.resolveConfirmTitle'), body: t('portal.detail.resolveConfirmBody') }))) return
  busy.value = kind
  try {
    const path = kind === 'resolve' ? '/portal/tickets/{id}/resolve' : '/portal/tickets/{id}/reopen'
    ticket.value = await unwrap(api.POST(path, { params: { path: { id: ticket.value.id } } }))
    ui.notify(kind === 'resolve' ? t('portal.detail.resolved') : t('portal.detail.reopened'), 'success')
  } catch (e) {
    ui.notify(e instanceof ApiProblem ? e.userMessage : t('errors.generic'), 'error')
    void load()
  } finally {
    busy.value = null
  }
}

function onSent(m: CustomerMessage) {
  appendMessage(m)
  // A reply on "awaiting your reply" re-opens the ticket server-side.
  if (ticket.value?.status === 'awaiting_reply' || ticket.value?.status === 'resolved') void load()
}

function onFollowUp(newId: string) {
  ui.notify(t('portal.detail.followUpCreated'), 'success')
  void router.push(`/tickets/${newId}`)
}

function onRated(tk: CustomerTicket | null) {
  rated.value = true
  if (tk && ticket.value) ticket.value = { ...ticket.value, ...tk }
}
</script>

<template>
  <v-container class="py-6" style="max-width: 960px">
    <v-btn to="/tickets" variant="text" :prepend-icon="mdiArrowLeft" class="mb-2 px-1">{{ $t('nav.myTickets') }}</v-btn>

    <v-progress-linear v-if="loading" indeterminate color="primary" />
    <EmptyState v-else-if="error || !ticket" :title="error ?? $t('portal.detail.notFound')" body="" data-testid="detail-error">
      <v-btn to="/tickets" color="primary">{{ $t('nav.myTickets') }}</v-btn>
    </EmptyState>

    <template v-else>
      <header class="fd-portal-detail-header d-flex flex-wrap align-center ga-2 mb-2">
        <div class="fd-pdh-meta d-flex flex-wrap align-center ga-2">
          <TicketNumber :number="ticket.number" :reftext="ticket.ref" />
          <ProjectChip :project="ticket.project" size="small" />
          <StatusChip :status="ticket.status" area="portal" size="default" />
          <PriorityChip :priority="ticket.priority" />
        </div>
        <h1 class="fd-pdh-subject text-h5" data-testid="detail-subject">{{ ticket.subject }}</h1>
      </header>
      <div class="text-body-2 text-medium-emphasis mb-4">
        {{ $t('portal.detail.openedBy', { name: ticket.requester?.name ?? '' }) }} · <RelativeTime :value="ticket.created_at" />
        <template v-if="ticket.followup_of_id">
          · <router-link :to="`/tickets/${ticket.followup_of_id}`" data-testid="followup-of">{{ $t('portal.detail.followUpOf') }}</router-link>
        </template>
      </div>

      <div class="fd-portal-detail-actions d-flex flex-wrap ga-2 mb-4">
        <v-btn
          v-if="can('resolve')"
          color="success"
          variant="tonal"
          :prepend-icon="mdiCheckCircleOutline"
          :loading="busy === 'resolve'"
          data-testid="resolve-btn"
          @click="act('resolve')"
        >
          {{ $t('portal.detail.resolve') }}
        </v-btn>
        <v-btn
          v-if="can('reopen')"
          variant="tonal"
          :prepend-icon="mdiRestore"
          :loading="busy === 'reopen'"
          data-testid="reopen-btn"
          @click="act('reopen')"
        >
          {{ $t('portal.detail.reopen') }}
        </v-btn>
      </div>

      <CustomFieldValues :defs="defs" :values="ticket.custom_fields" class="mb-4" />

      <v-alert v-if="followUpId" type="info" variant="tonal" class="mb-4" data-testid="followup-link">
        {{ $t('portal.detail.followUpExists') }}
        <router-link :to="`/tickets/${followUpId}`">{{ $t('portal.detail.openFollowUp') }}</router-link>
      </v-alert>

      <MessageThread :messages="thread" area="portal" class="mb-6" />

      <v-card v-if="can('csat') && ticket.csat_score == null && !rated" border flat class="pa-4 mb-6" data-testid="csat-card">
        <CsatForm :ticket-id="ticket.id" @rated="onRated" />
      </v-card>
      <v-alert v-else-if="rated" type="success" variant="tonal" class="mb-6" data-testid="csat-thanks">{{ $t('portal.csat.thanks') }}</v-alert>

      <v-card v-if="can('reply')" border flat class="pa-4">
        <v-alert v-if="closed" type="info" variant="tonal" density="compact" :icon="mdiInformationOutline" class="mb-3" data-testid="closed-notice">
          {{ $t('portal.detail.closedNotice') }}
        </v-alert>
        <ReplyForm :ticket-id="ticket.id" :closed="closed" @sent="onSent" @followup="onFollowUp" />
      </v-card>
    </template>
  </v-container>
</template>

<style scoped>
.fd-pdh-subject { flex: 1 1 200px; min-width: 0; margin: 0; line-height: 1.25; }
/* Phone: ref, project and chips on one row; full subject below; resolve/reopen full width. */
@media (max-width: 599.98px) {
  .fd-pdh-meta { flex: 1 1 100%; }
  .fd-pdh-subject { flex-basis: 100%; font-size: 1.25rem; font-weight: 500; line-height: 1.35; overflow-wrap: anywhere; }
  .fd-portal-detail-actions > .v-btn { flex: 1 1 100%; }
}
</style>
