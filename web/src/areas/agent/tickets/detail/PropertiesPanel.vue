<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Properties side panel (FE-023, FE-039, FE-054). Every control is enabled
 * only when its action is in the ticket's `allowed_actions` — the UI never
 * re-derives permissions. Changes are emitted as PATCH bodies; the controls
 * read the ticket prop, so a failed PATCH leaves them on the server value.
 *
 * Tags have no action of their own in `AllowedAction`; the permission matrix
 * groups "category / tags", so tags follow `set_category`.
 */
import { computed, ref } from 'vue'
import { mdiAccountArrowLeft, mdiArrowDownBold, mdiArrowUpBold, mdiEyeOffOutline, mdiEyeOutline } from '@mdi/js'
import { useSessionStore } from '@/stores/session'
import { useConfigStore } from '@/stores/config'
import TierChip from '@/components/TierChip.vue'
import UserAvatar from '@/components/UserAvatar.vue'
import SlaBadge from './SlaBadge.vue'
import CustomFieldsForm from './CustomFieldsForm.vue'
import { assignable } from './ticketApi'
import { useLookupsStore } from '../../shared/lookups'
import { fieldApplies } from '@/utils/customFields'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ ticket: S['Ticket']; saveFields: (v: S['CustomFieldValues']) => Promise<unknown> }>()
const emit = defineEmits<{
  patch: [S['TicketPatch']]
  escalate: ['escalate' | 'deescalate']
  watch: [userId: string, on: boolean]
}>()
const session = useSessionStore()
const config = useConfigStore()
const lookups = useLookupsStore()
const can = (a: string) => session.can(a, props.ticket)

const STATUSES: S['TicketStatus'][] = ['new', 'open', 'pending', 'on_hold', 'resolved', 'closed']
const PRIORITIES: S['Priority'][] = ['low', 'normal', 'high', 'urgent']

const staff = ref<S['StaffRef'][]>([])
const staffLoaded = ref(false)
const staffLoading = ref(false)
/** The picker lists only the users `/agent/assignable` returns (M-4 is server-side). */
async function loadAssignable() {
  if (staffLoaded.value || staffLoading.value) return
  staffLoading.value = true
  try {
    staff.value = await assignable(props.ticket.id)
    staffLoaded.value = true
  } catch {
    staff.value = []
  } finally {
    staffLoading.value = false
  }
}
const assigneeItems = computed(() => {
  const a = props.ticket.assignee
  const list = staff.value.map((s) => ({ value: s.id, title: s.name, subtitle: config.tierLabel(s.tier) }))
  if (a && !list.some((i) => i.value === a.id)) list.unshift({ value: a.id, title: a.name, subtitle: '' })
  return list
})
const isMine = computed(() => props.ticket.assignee?.id === session.me?.id)
function assign(id: string | null) {
  if ((id ?? null) === (props.ticket.assignee?.id ?? null)) return
  emit('patch', { assignee_id: id })
}

const categoryItems = computed(() => lookups.categories.filter((c) => c.active || c.id === props.ticket.category_id))
const tagItems = computed(() => lookups.tags.map((t) => t.name))
const watching = computed(() => !!props.ticket.watchers?.some((w) => w.id === session.me?.id))

/**
 * Definitions that apply to the ticket's category and project (no scope =
 * all), active only: the editor shows and sends only these, so a category
 * change drops the fields that stopped applying from the next save.
 */
const fieldDefs = computed(() =>
  lookups.customFields
    .filter((d) => d.active && fieldApplies(d, props.ticket.category_id, props.ticket.project_id))
    .sort((a, b) => a.sort - b.sort),
)
</script>

<template>
  <div class="d-flex flex-column ga-3" data-testid="properties">
    <v-card border flat>
      <v-card-text class="d-flex flex-column ga-1">
        <v-select
          :model-value="ticket.status"
          :items="STATUSES.map((s) => ({ value: s, title: $t(`status.${s}`) }))"
          :label="$t('agent.common.status')"
          :disabled="!can('set_status')"
          density="compact"
          hide-details
          class="mb-2"
          data-testid="prop-status"
          @update:model-value="(v: S['TicketStatus']) => v !== ticket.status && emit('patch', { status: v })"
        />
        <v-select
          :model-value="ticket.priority"
          :items="PRIORITIES.map((p) => ({ value: p, title: $t(`priority.${p}`) }))"
          :label="$t('agent.ticket.props.priority')"
          :disabled="!can('set_priority')"
          density="compact"
          hide-details
          class="mb-2"
          data-testid="prop-priority"
          @update:model-value="(v: S['Priority']) => v !== ticket.priority && emit('patch', { priority: v })"
        />
        <v-select
          :model-value="ticket.category_id ?? null"
          :items="categoryItems"
          item-title="name"
          item-value="id"
          :label="$t('agent.common.category')"
          :disabled="!can('set_category')"
          clearable
          density="compact"
          hide-details
          class="mb-2"
          data-testid="prop-category"
          @update:model-value="(v: string | null) => v !== (ticket.category_id ?? null) && emit('patch', { category_id: v })"
        />
        <v-select
          :model-value="ticket.team_id ?? null"
          :items="lookups.teams"
          item-title="name"
          item-value="id"
          :label="$t('agent.common.team')"
          :disabled="!can('transfer')"
          :hint="$t('agent.ticket.props.transferHint')"
          persistent-hint
          density="compact"
          class="mb-1"
          data-testid="prop-team"
          @update:model-value="(v: string | null) => v !== (ticket.team_id ?? null) && emit('patch', { team_id: v })"
        />
        <v-autocomplete
          :model-value="ticket.assignee?.id ?? null"
          :items="assigneeItems"
          :label="$t('agent.ticket.props.assignee')"
          :disabled="!can('assign')"
          :clearable="can('unassign') || can('assign')"
          :loading="staffLoading"
          density="compact"
          hide-details
          data-testid="prop-assignee"
          @focus="loadAssignable"
          @update:menu="(o: boolean) => o && loadAssignable()"
          @update:model-value="assign"
        />
        <div class="d-flex justify-end">
          <v-btn
            v-if="can('assign') && !isMine"
            size="small"
            variant="text"
            :prepend-icon="mdiAccountArrowLeft"
            data-testid="assign-me"
            @click="emit('patch', { assignee_id: session.me!.id })"
          >
            {{ $t('agent.ticket.props.assignMe') }}
          </v-btn>
        </div>
        <div class="d-flex align-center ga-2 flex-wrap">
          <span class="text-body-2 text-medium-emphasis">{{ $t('tier.label') }}</span>
          <TierChip :tier="ticket.tier" />
          <v-spacer />
          <v-btn v-if="can('deescalate')" size="small" variant="tonal" :prepend-icon="mdiArrowDownBold" data-testid="deescalate-open" @click="emit('escalate', 'deescalate')">
            {{ $t('agent.ticket.escalate.deescalate') }}
          </v-btn>
          <v-btn v-if="can('escalate')" size="small" color="warning" variant="tonal" :prepend-icon="mdiArrowUpBold" data-testid="escalate-open" @click="emit('escalate', 'escalate')">
            {{ $t('agent.ticket.escalate.escalate') }}
          </v-btn>
        </div>
      </v-card-text>
    </v-card>

    <v-card border flat :title="$t('agent.ticket.sla.title')" density="compact">
      <v-card-text class="d-flex flex-column ga-2 align-start">
        <SlaBadge :label="$t('agent.ticket.sla.firstResponse')" :start="ticket.created_at" :due="ticket.first_response_due_at" :met-at="ticket.first_responded_at" :paused-at="ticket.sla_paused_at" :breached="ticket.sla_breached && !ticket.first_responded_at" />
        <SlaBadge :label="$t('agent.ticket.sla.resolution')" :start="ticket.created_at" :due="ticket.resolution_due_at" :met-at="ticket.resolved_at" :paused-at="ticket.sla_paused_at" :breached="ticket.sla_breached" />
        <span v-if="!ticket.first_response_due_at && !ticket.resolution_due_at" class="text-body-2 text-medium-emphasis">{{ $t('agent.ticket.sla.none') }}</span>
      </v-card-text>
    </v-card>

    <v-card border flat :title="$t('agent.ticket.props.requester')" density="compact">
      <v-card-text>
        <router-link :to="`/agent/customers/${ticket.requester.id}`" class="d-flex align-center ga-2 text-decoration-none" data-testid="requester-link">
          <UserAvatar :name="ticket.requester.name" :size="28" />
          <span>{{ ticket.requester.name }}</span>
        </router-link>
        <router-link v-if="ticket.organization_id" :to="`/agent/organizations/${ticket.organization_id}`" class="d-block mt-2 text-body-2">
          {{ $t('agent.ticket.props.organization') }}
        </router-link>
      </v-card-text>
    </v-card>

    <v-card border flat :title="$t('agent.ticket.props.tags')" density="compact">
      <v-card-text>
        <v-combobox
          :model-value="ticket.tags ?? []"
          :items="tagItems"
          :label="$t('agent.ticket.props.tags')"
          multiple
          chips
          closable-chips
          :disabled="!can('set_category')"
          density="compact"
          hide-details
          data-testid="prop-tags"
          @update:model-value="(v: string[]) => emit('patch', { tags: v.map((x) => x.trim()).filter(Boolean) })"
        />
      </v-card-text>
    </v-card>

    <v-card border flat :title="$t('agent.ticket.watchers.title')" density="compact">
      <v-card-text>
        <div class="d-flex flex-wrap ga-1 mb-2" data-testid="watchers">
          <v-chip
            v-for="w in ticket.watchers ?? []"
            :key="w.id"
            size="small"
            :closable="can('watch')"
            data-testid="watcher-chip"
            @click:close="emit('watch', w.id, false)"
          >
            {{ w.name }}
          </v-chip>
          <span v-if="!ticket.watchers?.length" class="text-body-2 text-medium-emphasis">{{ $t('agent.common.none') }}</span>
        </div>
        <div v-if="can('watch')" class="d-flex flex-wrap ga-2 align-center">
          <v-btn size="small" variant="tonal" :prepend-icon="watching ? mdiEyeOffOutline : mdiEyeOutline" data-testid="watch-toggle" @click="emit('watch', session.me!.id, !watching)">
            {{ watching ? $t('agent.ticket.watchers.unwatch') : $t('agent.ticket.watchers.watch') }}
          </v-btn>
          <v-autocomplete
            :items="lookups.staff.filter((s) => !ticket.watchers?.some((w) => w.id === s.id))"
            item-title="name"
            item-value="id"
            :label="$t('agent.ticket.watchers.add')"
            density="compact"
            hide-details
            class="flex-grow-1"
            :model-value="null"
            data-testid="watcher-add"
            @update:model-value="(id: string | null) => id && emit('watch', id, true)"
          />
        </div>
      </v-card-text>
    </v-card>

    <v-card v-if="fieldDefs.length" border flat :title="$t('agent.ticket.props.customFields')" density="compact">
      <v-card-text>
        <CustomFieldsForm :defs="fieldDefs" :values="ticket.custom_fields" :editable="can('edit_fields')" :save="saveFields" />
      </v-card-text>
    </v-card>
  </div>
</template>
