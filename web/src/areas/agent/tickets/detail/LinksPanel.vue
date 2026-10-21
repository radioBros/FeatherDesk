<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Links panel (FE-055): parent / children / related / duplicates with status chips; add and remove per `link`. */
import { computed, ref } from 'vue'
import { mdiClose, mdiPlus } from '@mdi/js'
import { useSessionStore } from '@/stores/session'
import StatusChip from '@/components/StatusChip.vue'
import LinkForm from './LinkForm.vue'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ ticket: S['Ticket'] }>()
const emit = defineEmits<{ added: [S['TicketLink']]; remove: [S['TicketLink']] }>()
const session = useSessionStore()
const dialog = ref(false)

type Group = 'children' | 'parent' | 'related' | 'duplicateOf' | 'duplicatedBy'
/** Direction matters: `parent_of` from this ticket lists a child; towards it, the parent. */
function groupOf(l: S['TicketLink']): Group {
  const outgoing = l.from_ticket_id === props.ticket.id
  if (l.type === 'parent_of') return outgoing ? 'children' : 'parent'
  if (l.type === 'duplicate_of') return outgoing ? 'duplicateOf' : 'duplicatedBy'
  return 'related'
}
const groups = computed(() => {
  const out: Partial<Record<Group, S['TicketLink'][]>> = {}
  for (const l of props.ticket.links ?? []) (out[groupOf(l)] ??= []).push(l)
  return out
})
const ORDER: Group[] = ['parent', 'children', 'related', 'duplicateOf', 'duplicatedBy']

function onSaved(l: S['TicketLink']) {
  dialog.value = false
  emit('added', l)
}
</script>

<template>
  <v-card border flat :title="$t('agent.ticket.links.title')" density="compact" data-testid="links-panel">
    <template v-if="session.can('link', ticket)" #append>
      <v-btn :icon="mdiPlus" size="small" variant="text" :aria-label="$t('agent.ticket.links.add')" data-testid="link-add" @click="dialog = true" />
    </template>
    <v-card-text>
      <template v-for="g in ORDER" :key="g">
        <div v-if="groups[g]?.length" class="mb-2">
          <div class="text-caption text-medium-emphasis">{{ $t(`agent.ticket.links.group.${g}`) }}</div>
          <div v-for="l in groups[g]" :key="l.id" class="d-flex align-center ga-2" data-testid="link-row">
            <router-link v-if="l.to_ticket" :to="`/agent/tickets/${l.to_ticket.ref}`" class="text-truncate">
              {{ l.to_ticket.ref }} {{ l.to_ticket.subject }}
            </router-link>
            <StatusChip v-if="l.to_ticket" :status="l.to_ticket.status" size="x-small" />
            <v-spacer />
            <v-btn v-if="session.can('link', ticket)" :icon="mdiClose" size="x-small" variant="text" :aria-label="$t('agent.common.remove')" @click="emit('remove', l)" />
          </div>
        </div>
      </template>
      <span v-if="!ticket.links?.length" class="text-body-2 text-medium-emphasis">{{ $t('agent.common.none') }}</span>
    </v-card-text>
    <v-dialog v-model="dialog" max-width="520">
      <v-card :title="$t('agent.ticket.links.add')">
        <v-card-text><LinkForm v-if="dialog" :ticket-id="ticket.id" @saved="onSaved" @cancel="dialog = false" /></v-card-text>
      </v-card>
    </v-dialog>
  </v-card>
</template>
