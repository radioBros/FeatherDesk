<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Reply / internal-note composer (FE-022, FE-039, FE-045, FE-052).
 *
 * - Reply vs Internal note toggle, each offered only when in `allowed_actions`;
 *   the note mode is visually distinct and switching a draft to public warns.
 * - "Send and set status" (pending, on hold, resolved): one request.
 * - One `Idempotency-Key` per draft: a retry after a failure re-sends the same
 *   key, so the server never stores the message twice.
 * - A public reply that still contains a link to an internal KB article is
 *   blocked by a validation rule.
 * - 409 `invalid-transition` keeps the draft and hands the allowed statuses to
 *   the page (`transition` event); `resend(status)` sends again.
 */
import { computed, reactive, ref, watch } from 'vue'
import { mdiChevronUp, mdiLockOutline, mdiSend } from '@mdi/js'
import { useForm } from '@/composables/useForm'
import { validations, type Rule } from '@/constants/validations'
import { newIdempotencyKey } from '@/api/client'
import { useSessionStore } from '@/stores/session'
import MarkdownEditor from '@/components/MarkdownEditor.vue'
import AttachmentUploader from '@/components/AttachmentUploader.vue'
import CannedPicker from './CannedPicker.vue'
import KbLinkPicker from './KbLinkPicker.vue'
import { mentionAt } from './composer'
import { sendMessage, isInvalidTransition, allowedFrom } from './ticketApi'
import { useLookupsStore } from '../../shared/lookups'
import type { components } from '@/api/schema'

type S = components['schemas']
type Mode = 'public' | 'internal'

const props = defineProps<{
  ticket: Pick<S['Ticket'], 'id' | 'number' | 'allowed_actions'> & { requester?: S['UserRef'] }
  /** Names of other staff currently typing on this ticket (presence, FE-046). */
  typing?: string[]
}>()
const emit = defineEmits<{
  sent: [message: S['Message'], status: S['TicketStatus'] | null]
  transition: [allowed: S['TicketStatus'][]]
  typing: []
  idle: []
}>()

const session = useSessionStore()
const lookups = useLookupsStore()
const canReply = computed(() => session.can('reply', props.ticket))
const canNote = computed(() => session.can('note', props.ticket))
const mode = ref<Mode>(canReply.value ? 'public' : 'internal')
const model = reactive({ body: '', attachments: [] as string[] })
const internalLinks = ref<string[]>([])
const switchedToPublic = ref(false)
const nextStatus = ref<S['TicketStatus'] | null>(null)
let draftKey: string | null = null

const editor = ref<InstanceType<typeof MarkdownEditor> | null>(null)
const uploader = ref<InstanceType<typeof AttachmentUploader> | null>(null)
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const linkRules: Rule[] = [validations.noInternalLinks(() => internalLinks.value, () => mode.value === 'public')]
// Same placeholders as the server preview (TKT-023): `customer.name` is
// canonical, `requester.name` an alias.
const vars = computed(() => ({
  'customer.name': props.ticket.requester?.name ?? '',
  'requester.name': props.ticket.requester?.name ?? '',
  'ticket.number': String(props.ticket.number),
  'agent.name': session.me?.name ?? '',
}))

watch(mode, (m, old) => {
  switchedToPublic.value = m === 'public' && old === 'internal' && !!model.body.trim()
})
watch(
  () => model.body,
  (v) => {
    if (v) draftKey ??= newIdempotencyKey()
    if (v.trim()) emit('typing')
    updateMention()
  },
)

const STATUSES: S['TicketStatus'][] = ['pending', 'on_hold', 'resolved']

async function doSend(status: S['TicketStatus'] | null) {
  draftKey ??= newIdempotencyKey()
  try {
    const msg = await sendMessage(
      props.ticket.id,
      {
        kind: mode.value,
        body_md: model.body,
        ...(model.attachments.length ? { attachment_ids: model.attachments } : {}),
        ...(status ? { set_status: status } : {}),
      },
      draftKey,
    )
    emit('sent', msg, status)
    model.body = ''
    model.attachments = []
    internalLinks.value = []
    switchedToPublic.value = false
    draftKey = null
    uploader.value?.clear()
    formRef.value?.resetValidation?.()
    emit('idle')
  } catch (e) {
    // The draft (and its idempotency key) stays for the retry.
    if (isInvalidTransition(e)) {
      emit('transition', allowedFrom(e))
      return
    }
    throw e
  }
}

const onSubmit = submit(() => doSend(nextStatus.value))

function send(status: S['TicketStatus'] | null = null) {
  nextStatus.value = status
  return onSubmit()
}

/** After a 409, the page re-sends with a status the agent picked (or none). */
function resend(status: S['TicketStatus'] | null) {
  return send(status)
}

function setMode(m: Mode) {
  if ((m === 'public' && canReply.value) || (m === 'internal' && canNote.value)) mode.value = m
}

function focus() {
  editor.value?.focus()
}

function insertKb(link: { markdown: string; href: string; internal: boolean }) {
  if (link.internal) internalLinks.value = [...internalLinks.value, link.href]
  void editor.value?.insert(link.markdown)
}

// @mention autocomplete in internal notes (FE-039), from the staff directory.
const mention = ref<string | null>(null)
const mentionOptions = computed(() => {
  const q = (mention.value ?? '').toLowerCase()
  return lookups.staff.filter((s) => s.name.toLowerCase().includes(q)).slice(0, 8)
})
function updateMention() {
  if (mode.value !== 'internal') return (mention.value = null)
  const caret = editor.value?.cursor() ?? model.body.length
  mention.value = mentionAt(model.body, caret)
  if (mention.value !== null) void lookups.ensure('staff')
}
function pickMention(name: string) {
  const typed = mention.value ?? ''
  mention.value = null
  void editor.value?.insert(`@${name.replace(/\s+/g, '.')} `, typed.length + 1)
}

defineExpose({ setMode, focus, resend, send })
</script>

<template>
  <v-form
    ref="formRef"
    :class="['st-composer', 'pa-3', 'rounded', mode === 'internal' ? 'st-composer--note bg-internal-note' : 'border']"
    data-testid="composer"
    @submit.prevent="onSubmit"
  >
    <div class="d-flex flex-wrap align-center ga-2 mb-2">
      <v-btn-toggle :model-value="mode" density="compact" mandatory variant="outlined" divided @update:model-value="setMode">
        <v-btn v-if="canReply" value="public" data-testid="mode-public">{{ $t('agent.ticket.composer.reply') }}</v-btn>
        <v-btn v-if="canNote" value="internal" :prepend-icon="mdiLockOutline" data-testid="mode-internal">
          {{ $t('agent.ticket.composer.note') }}
        </v-btn>
      </v-btn-toggle>
      <v-spacer />
      <CannedPicker :vars="vars" @insert="(t: string) => editor?.insert(t)" />
      <KbLinkPicker :mode="mode" @insert="insertKb" />
    </div>

    <v-alert v-if="typing?.length" type="warning" variant="tonal" density="compact" class="mb-2" data-testid="typing-warning">
      {{ $t('agent.ticket.presence.replying', { names: typing.join(', ') }, typing.length) }}
    </v-alert>
    <v-alert v-if="mode === 'internal'" type="info" variant="text" density="compact" class="mb-1 pa-0" :icon="mdiLockOutline">
      {{ $t('agent.ticket.composer.noteHint') }}
    </v-alert>
    <v-alert v-if="switchedToPublic" type="warning" variant="tonal" density="compact" class="mb-2" data-testid="public-warning">
      {{ $t('agent.ticket.composer.publicWarning') }}
    </v-alert>
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-2">{{ formError }}</v-alert>

    <MarkdownEditor
      ref="editor"
      v-model="model.body"
      required
      :rows="4"
      :extra-rules="linkRules"
      :error-messages="fieldErrors.body_md"
      :label="mode === 'internal' ? $t('agent.ticket.composer.noteLabel') : $t('agent.ticket.composer.replyLabel')"
      testid="composer-body"
      @submit="onSubmit"
      @files="(f: File[]) => uploader?.add(f)"
    />
    <v-list v-if="mention !== null && mentionOptions.length" density="compact" border rounded class="mb-2" data-testid="mention-list">
      <v-list-item v-for="s in mentionOptions" :key="s.id" :title="s.name" @click="pickMention(s.name)" />
    </v-list>

    <div class="d-flex flex-wrap align-center ga-2">
      <AttachmentUploader ref="uploader" v-model="model.attachments" area="agent" />
      <v-spacer />
      <v-btn-group color="primary" density="comfortable" divided>
        <v-btn type="submit" :loading="submitting" :prepend-icon="mdiSend" data-testid="composer-send" @click="nextStatus = null">
          {{ mode === 'internal' ? $t('agent.ticket.composer.addNote') : $t('agent.ticket.composer.send') }}
        </v-btn>
        <v-menu v-if="session.can('set_status', ticket)" location="top end">
          <template #activator="{ props: p }">
            <v-btn v-bind="p" :icon="mdiChevronUp" :aria-label="$t('agent.ticket.composer.sendAnd')" data-testid="composer-send-menu" />
          </template>
          <v-list density="compact">
            <v-list-subheader>{{ $t('agent.ticket.composer.sendAnd') }}</v-list-subheader>
            <v-list-item
              v-for="s in STATUSES"
              :key="s"
              :title="$t(`status.${s}`)"
              :data-testid="`send-status-${s}`"
              @click="send(s)"
            />
          </v-list>
        </v-menu>
      </v-btn-group>
    </div>
  </v-form>
</template>

<style scoped>
.st-composer--note { border: 1px dashed rgb(var(--v-theme-on-internal-note)); }
</style>
