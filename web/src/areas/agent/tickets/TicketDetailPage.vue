<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/tickets/:number — ticket detail (FE-022…FE-024, FE-039, FE-045,
 * FE-046, FE-052 KB link, FE-054 fields, FE-055 links/merge/split).
 *
 * Live over `ticket:<id>`: new messages are appended from the frame (no HTTP),
 * other changes trigger one debounced refetch; `ticket.access_lost` and any
 * mutation answering `visible: false` navigate back to the list with a toast.
 */
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { mdiArrowLeft, mdiCallMerge, mdiCallSplit, mdiCheckCircleOutline, mdiDotsVertical, mdiFolderSwapOutline, mdiPencil, mdiRestore, mdiSourceBranch } from '@mdi/js'
import { useSessionStore } from '@/stores/session'
import { useUiStore } from '@/stores/ui'
import { useRealtimeStore } from '@/stores/realtime'
import { useRealtime } from '@/composables/useRealtime'
import { ApiProblem } from '@/api/problem'
import MessageThread from '@/components/MessageThread.vue'
import StatusChip from '@/components/StatusChip.vue'
import PriorityChip from '@/components/PriorityChip.vue'
import TierChip from '@/components/TierChip.vue'
import TicketNumber from '@/components/TicketNumber.vue'
import ProjectChip from '@/components/ProjectChip.vue'
import MoveProjectForm from './detail/MoveProjectForm.vue'
import { useProjectsStore } from '@/stores/projects'
import UserAvatar from '@/components/UserAvatar.vue'
import EmptyState from '@/components/EmptyState.vue'
import ComposerForm from './detail/ComposerForm.vue'
import PropertiesPanel from './detail/PropertiesPanel.vue'
import LinksPanel from './detail/LinksPanel.vue'
import TimelineTab from './detail/TimelineTab.vue'
import EscalateForm from './detail/EscalateForm.vue'
import MergeForm from './detail/MergeForm.vue'
import SplitForm from './detail/SplitForm.vue'
import EditNoteForm from './detail/EditNoteForm.vue'
import TransitionDialog from './detail/TransitionDialog.vue'
import StaleDialog from './detail/StaleDialog.vue'
import MacroPicker from '../automation/MacroPicker.vue'
import { useTicketDetail, type Outcome } from './detail/useTicketDetail'
import * as ticketApi from './detail/ticketApi'
import TwoColumnLayout from '@/components/TwoColumnLayout.vue'
import { useLookupsStore } from '../shared/lookups'
import { useShortcuts } from '../shared/useShortcuts'
import type { PresenceUser } from '@/realtime/client'
import type { Message } from '@/types'
import type { components } from '@/api/schema'

type S = components['schemas']
const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const session = useSessionStore()
const ui = useUiStore()
const lookups = useLookupsStore()
const projects = useProjectsStore()
const rt = useRealtimeStore()
const detail = useTicketDetail()
const { ticket } = detail

const tab = ref<'thread' | 'timeline'>('thread')
const timelineKey = ref(0)
const composer = ref<InstanceType<typeof ComposerForm> | null>(null)
const escalateDir = ref<'escalate' | 'deescalate' | null>(null)
const mergeOpen = ref(false)
const moveOpen = ref(false)
const splitMode = ref(false)
const splitSelected = ref<string[]>([])
const editing = ref<S['Message'] | null>(null)
const splitChild = ref<S['Ticket'] | null>(null)

/** The route's key: a project ref (`/agent/tickets/WEB-12`) or the legacy global number. */
const key = computed<string | number | null>(() => {
  const r = route.params.ref
  if (typeof r === 'string' && r) return r
  const n = Number(route.params.number)
  return n ? n : null
})
const can = (a: string) => session.can(a, ticket.value)
/** Moving needs the server's `transfer` action and another active project to move to. */
const canMove = computed(() => can('transfer') && projects.active.some((p) => p.id !== ticket.value?.project_id))

void projects.load()
void lookups.ensure('teams', 'categories', 'customFields', 'tags', 'staff')
watch(key, (k) => k && void detail.load(k), { immediate: true })

function leave(message = t('agent.ticket.lostAccess')) {
  ui.notify(message, 'warning')
  void router.push('/agent/tickets')
}
function onOutcome(o: Outcome) {
  if (o === 'lost') leave()
  else if (o === 'ok') timelineKey.value++
}

// ---- realtime: ticket topic + presence (FE-044, FE-046)
const topic = computed(() => (ticket.value ? `ticket:${ticket.value.id}` : null))
useRealtime(() => (topic.value ? [topic.value] : []), {
  onEvent: (ev) => {
    if (ev.event === 'ticket.access_lost') return leave()
    const m = (ev.data as { message?: S['Message'] } | undefined)?.message
    if ((ev.event === 'message.created' || ev.event === 'note.created') && m) detail.appendMessage(m)
    else detail.scheduleRefetch(() => leave())
    timelineKey.value++
  },
  onResync: () => void detail.refetch(),
})

const others = ref<PresenceUser[]>([])
const typingNames = computed(() => others.value.filter((u) => u.state === 'typing').map((u) => u.name ?? '?'))
const offPresence = rt.client?.onPresence((tp, users) => {
  if (tp === topic.value) others.value = users.filter((u) => u.id !== session.me?.id)
})
let lastTyping = 0
function presence(state: 'viewing' | 'typing' | 'idle') {
  if (topic.value) rt.client?.sendPresence(topic.value, state)
}
function onTyping() {
  const now = Date.now()
  // ≤ 1 typing frame per 3 s (FE-046).
  if (now - lastTyping < 3000) return
  lastTyping = now
  presence('typing')
}
function onIdle() {
  lastTyping = 0
  presence('idle')
}
watch([topic, () => rt.connected], ([tp, up], [oldTp]) => {
  if (oldTp && oldTp !== tp) rt.client?.sendPresence(oldTp, 'idle')
  if (tp && up) presence('viewing')
})
const onBlur = () => presence('idle')
const onFocus = () => presence('viewing')
window.addEventListener('blur', onBlur)
window.addEventListener('focus', onFocus)
onBeforeUnmount(() => {
  presence('idle')
  offPresence?.()
  window.removeEventListener('blur', onBlur)
  window.removeEventListener('focus', onFocus)
  detail.dispose()
})

// ---- shortcuts: r reply, n note, e escalate (FE-038)
useShortcuts({
  r: () => {
    tab.value = 'thread'
    composer.value?.setMode('public')
    composer.value?.focus()
  },
  n: () => {
    tab.value = 'thread'
    composer.value?.setMode('internal')
    composer.value?.focus()
  },
  e: () => {
    if (can('escalate')) escalateDir.value = 'escalate'
  },
})

// ---- mutations
const openChildren = computed(() =>
  (ticket.value?.links ?? []).filter(
    (l) => l.type === 'parent_of' && l.from_ticket_id === ticket.value?.id && l.to_ticket && !['resolved', 'closed'].includes(l.to_ticket.status),
  ),
)
// One-click resolve / reopen (owner request): the same PATCH as the status field,
// so open-children warnings, 409 transitions and stale ETags behave the same.
const resolving = ref(false)
const canResolve = computed(() => !!ticket.value && can('set_status') && !['resolved', 'closed'].includes(ticket.value.status))
const canReopen = computed(() => !!ticket.value && can('set_status') && ticket.value.status === 'resolved')
async function setStatus(status: S['TicketStatus']) {
  resolving.value = true
  try {
    await onPatch({ status })
  } finally {
    resolving.value = false
  }
}

async function onPatch(body: S['TicketPatch']) {
  // Resolving a parent with open children is allowed; warn without blocking (lifecycle §2b).
  if (body.status === 'resolved' && openChildren.value.length)
    ui.notify(t('agent.ticket.links.openChildren', { n: openChildren.value.length }, openChildren.value.length), 'warning')
  onOutcome(await detail.patch(body))
}

async function saveFields(values: S['CustomFieldValues']) {
  if (!ticket.value) return
  try {
    const { result, etag } = await ticketApi.patchTicket(ticket.value.id, { custom_fields: values }, detail.etag.value)
    onOutcome(detail.apply(result, etag))
    ui.notify(t('agent.common.saved'), 'success')
  } catch (e) {
    if (e instanceof ApiProblem && e.status === 422) throw e
    detail.handleError(e)
  }
}

function onSent(m: S['Message'], status: S['TicketStatus'] | null) {
  detail.appendMessage(m)
  if (status) void detail.refetch().then((ok) => !ok && leave())
  timelineKey.value++
}
function onComposerTransition(allowed: S['TicketStatus'][]) {
  detail.transition.value = {
    allowed,
    pick: (s) => {
      detail.transition.value = null
      if (s) void composer.value?.resend(s)
    },
    skip: () => {
      detail.transition.value = null
      void composer.value?.resend(null)
    },
  }
}

// ---- macros (FE-065): one transaction server-side; `visible: false` leaves like any mutation
function onMacroApplied(res: S['TicketUpdateResult'], etag: string | null, m: S['Macro']) {
  const o = detail.apply(res, etag)
  if (o === 'ok') ui.notify(t('automation.macros.applied', { name: m.name }), 'success')
  onOutcome(o)
}

function onEscalated(res: S['TicketUpdateResult']) {
  escalateDir.value = null
  if (!res.visible) return leave(t('agent.ticket.escalate.handedOver'))
  detail.apply(res)
  void detail.refetch()
  timelineKey.value++
}

async function onWatch(userId: string, on: boolean) {
  if (!ticket.value) return
  try {
    if (on) {
      const updated = await ticketApi.addWatcher(ticket.value.id, userId)
      ticket.value.watchers = updated.watchers
    } else {
      await ticketApi.removeWatcher(ticket.value.id, userId)
      ticket.value.watchers = (ticket.value.watchers ?? []).filter((w) => w.id !== userId)
    }
  } catch (e) {
    detail.handleError(e)
  }
}

async function onLinkAdded() {
  await detail.refetch()
  timelineKey.value++
}
async function onLinkRemove(l: S['TicketLink']) {
  if (!ticket.value) return
  try {
    await ticketApi.removeLink(ticket.value.id, l.id)
    ticket.value.links = (ticket.value.links ?? []).filter((x) => x.id !== l.id)
  } catch (e) {
    detail.handleError(e)
  }
}

function onMerged(target: S['TicketSummary']) {
  mergeOpen.value = false
  ui.notify(t('agent.ticket.merge.done', { target: target.ref ?? target.number }), 'success')
  void router.push(`/agent/tickets/${target.ref ?? target.number}`)
}

function onMoved(res: S['TicketUpdateResult'], etag: string | null) {
  moveOpen.value = false
  if (!res.visible) return leave(t('projects.ticket.move.handedOver'))
  detail.apply(res, etag)
  timelineKey.value++
  ui.notify(t('projects.ticket.move.done', { ref: res.ticket.ref }), 'success')
  // The old ref no longer resolves: continue on the new one.
  void router.replace(`/agent/tickets/${res.ticket.ref}`)
}

function onSplit(child: S['Ticket']) {
  splitMode.value = false
  splitSelected.value = []
  splitChild.value = child
  void detail.refetch()
}

function canEdit(m: Message) {
  // Own internal notes within 15 minutes (the API enforces the same window).
  return m.kind === 'internal' && m.author?.id === session.me?.id && Date.now() - Date.parse(m.created_at) < 15 * 60_000
}
function onNoteSaved(m: S['Message']) {
  editing.value = null
  detail.replaceMessage(m)
}

const isParent = computed(() => (ticket.value?.links ?? []).some((l) => l.type === 'parent_of' && l.from_ticket_id === ticket.value?.id))
const isChild = computed(() => (ticket.value?.links ?? []).some((l) => l.type === 'parent_of' && l.to_ticket_id === ticket.value?.id))
const messages = computed(() => (ticket.value?.messages ?? []) as unknown as Message[])
</script>

<template>
  <v-container fluid class="pa-4">
    <v-progress-linear v-if="detail.loading.value && !ticket" indeterminate />
    <EmptyState v-else-if="detail.notFound.value" :title="$t('errors.notFound')" :body="$t('agent.ticket.notFound')">
      <v-btn to="/agent/tickets" color="primary">{{ $t('nav.tickets') }}</v-btn>
    </EmptyState>

    <template v-else-if="ticket">
      <!-- Phone: back, ref and project on the first row (with ⋮ at the end), the full subject below, then the chips. -->
      <div class="fd-ticket-header d-flex flex-wrap align-center ga-2 mb-3" data-testid="ticket-header">
        <div class="fd-th-nav d-flex align-center ga-2">
          <v-btn :icon="mdiArrowLeft" variant="text" to="/agent/tickets" :aria-label="$t('app.back')" />
          <TicketNumber :number="ticket.number" :reftext="ticket.ref" />
          <ProjectChip :project="ticket.project" />
        </div>
        <h1 class="fd-th-title text-h6" data-testid="ticket-subject">{{ ticket.subject }}</h1>
        <div class="fd-th-chips d-flex flex-wrap align-center ga-2">
          <v-chip v-if="isParent" size="small" label :prepend-icon="mdiSourceBranch" data-testid="badge-parent">{{ $t('agent.ticket.links.parentBadge') }}</v-chip>
          <v-chip v-if="isChild" size="small" label :prepend-icon="mdiSourceBranch" data-testid="badge-child">{{ $t('agent.ticket.links.childBadge') }}</v-chip>
          <StatusChip :status="ticket.status" />
          <PriorityChip :priority="ticket.priority" />
          <TierChip :tier="ticket.tier" />
          <MacroPicker v-if="can('reply') || can('note') || can('set_status')" :ticket="ticket" :etag="detail.etag.value" @applied="onMacroApplied" @failed="detail.handleError" />
          <v-btn
            v-if="canResolve"
            color="success"
            variant="flat"
            :prepend-icon="mdiCheckCircleOutline"
            :loading="resolving"
            data-testid="ticket-resolve"
            @click="setStatus('resolved')"
          >{{ $t('agent.ticket.resolve') }}</v-btn>
          <v-btn
            v-else-if="canReopen"
            variant="tonal"
            :prepend-icon="mdiRestore"
            :loading="resolving"
            data-testid="ticket-reopen"
            @click="setStatus('open')"
          >{{ $t('agent.ticket.reopen') }}</v-btn>
        </div>
        <div class="fd-th-more d-flex align-center ga-2">
        <div v-if="others.length" class="d-flex ga-1" data-testid="presence" :aria-label="$t('agent.ticket.presence.viewing')">
          <v-tooltip v-for="u in others" :key="u.id" :text="`${u.name} · ${$t(`agent.ticket.presence.${u.state === 'typing' ? 'typing' : 'viewingOne'}`)}`">
            <template #activator="{ props: p }"><span v-bind="p"><UserAvatar :name="u.name" :size="28" /></span></template>
          </v-tooltip>
        </div>
        <v-menu v-if="can('merge') || can('split') || canMove">
          <template #activator="{ props: p }">
            <v-btn v-bind="p" :icon="mdiDotsVertical" variant="text" :aria-label="$t('agent.common.actions')" data-testid="ticket-more" />
          </template>
          <v-list density="compact">
 <v-list-item v-if="canMove" :prepend-icon="mdiFolderSwapOutline" :title="$t('projects.ticket.move.title')" data-testid="move-open" @click="moveOpen = true" />
            <v-list-item v-if="can('merge')" :prepend-icon="mdiCallMerge" :title="$t('agent.ticket.merge.title')" data-testid="merge-open" @click="mergeOpen = true" />
            <v-list-item v-if="can('split')" :prepend-icon="mdiCallSplit" :title="$t('agent.ticket.split.title')" data-testid="split-open" @click="splitMode = true; tab = 'thread'" />
          </v-list>
        </v-menu>
        </div>
      </div>

      <TwoColumnLayout>
        <template #main>
          <v-tabs v-model="tab" density="compact" class="mb-3">
            <v-tab value="thread" data-testid="tab-thread">{{ $t('agent.ticket.tabs.thread') }}</v-tab>
            <v-tab value="timeline" data-testid="tab-timeline">{{ $t('agent.ticket.tabs.timeline') }}</v-tab>
          </v-tabs>
          <div v-show="tab === 'thread'">
            <v-card v-if="splitMode" border flat class="mb-3 pa-3" data-testid="split-panel">
              <div class="text-subtitle-2 mb-2">{{ $t('agent.ticket.split.hint') }}</div>
              <SplitForm :ticket-id="ticket.id" :selected="splitSelected" @split="onSplit" @cancel="splitMode = false; splitSelected = []" />
            </v-card>
            <v-alert v-if="splitChild" type="success" variant="tonal" density="compact" class="mb-3" closable @click:close="splitChild = null">
              {{ $t('agent.ticket.split.done', { n: splitChild.ref ?? splitChild.number }) }}
              <template #append>
                <v-btn size="small" variant="text" :to="`/agent/tickets/${splitChild.ref ?? splitChild.number}`" data-testid="split-open-child">{{ $t('agent.common.open') }}</v-btn>
              </template>
            </v-alert>
            <MessageThread v-model:selected="splitSelected" :messages="messages" area="agent" :selectable="splitMode">
              <template #actions="{ message }">
                <v-btn
                  v-if="canEdit(message)"
                  :icon="mdiPencil"
                  size="small"
                  variant="text"
                  :aria-label="$t('agent.common.edit')"
                  data-testid="note-edit"
                  @click="editing = message as unknown as S['Message']"
                />
              </template>
            </MessageThread>
            <div class="mt-4">
              <ComposerForm
                v-if="can('reply') || can('note')"
                ref="composer"
                :ticket="ticket"
                :typing="typingNames"
                @sent="onSent"
                @transition="onComposerTransition"
                @typing="onTyping"
                @idle="onIdle"
              />
            </div>
          </div>
          <TimelineTab v-if="tab === 'timeline'" :ticket-id="ticket.id" :refresh-key="timelineKey" />
        </template>
        <template #aside>
          <PropertiesPanel :ticket="ticket" :save-fields="saveFields" @patch="onPatch" @escalate="(d) => (escalateDir = d)" @watch="onWatch" />
          <LinksPanel :ticket="ticket" @added="onLinkAdded" @remove="onLinkRemove" />
        </template>
      </TwoColumnLayout>

      <v-dialog :model-value="!!escalateDir" max-width="520" @update:model-value="(v: boolean) => !v && (escalateDir = null)">
        <v-card v-if="escalateDir" :title="escalateDir === 'escalate' ? $t('agent.ticket.escalate.titleUp') : $t('agent.ticket.escalate.titleDown')">
          <v-card-text>
            <EscalateForm :ticket="ticket" :direction="escalateDir" :teams="lookups.teams" @done="onEscalated" @cancel="escalateDir = null" />
          </v-card-text>
        </v-card>
      </v-dialog>
      <v-dialog v-model="mergeOpen" max-width="560">
        <v-card :title="$t('agent.ticket.merge.title')">
          <v-card-text><MergeForm v-if="mergeOpen" :ticket="ticket" @merged="onMerged" @cancel="mergeOpen = false" /></v-card-text>
        </v-card>
      </v-dialog>
      <v-dialog v-model="moveOpen" max-width="520">
        <v-card :title="$t('projects.ticket.move.title')">
          <v-card-text><MoveProjectForm v-if="moveOpen" :ticket="ticket" :etag="detail.etag.value" @moved="onMoved" @cancel="moveOpen = false" /></v-card-text>
        </v-card>
      </v-dialog>
      <v-dialog :model-value="!!editing" max-width="640" @update:model-value="(v: boolean) => !v && (editing = null)">
        <v-card v-if="editing" :title="$t('agent.ticket.editNote')">
          <v-card-text><EditNoteForm :ticket-id="ticket.id" :message="editing" @saved="onNoteSaved" @cancel="editing = null" /></v-card-text>
        </v-card>
      </v-dialog>
      <TransitionDialog :conflict="detail.transition.value" />
      <StaleDialog :open="detail.stale.value" @reload="detail.refetch()" @close="detail.stale.value = false" />
    </template>
  </v-container>
</template>

<style scoped>
.fd-th-title { flex: 1 1 200px; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; margin: 0; }
.fd-th-more { margin-inline-start: auto; }
/* Phone: row 1 = back, ref, project, ⋮ · row 2 = the full subject · row 3 = chips and actions. */
@media (max-width: 599.98px) {
  .fd-th-nav { order: 1; }
  .fd-th-more { order: 2; }
  .fd-th-title { order: 3; flex-basis: 100%; white-space: normal; overflow-wrap: anywhere; line-height: 1.3; }
  .fd-th-chips { order: 4; flex-basis: 100%; }
}
</style>
