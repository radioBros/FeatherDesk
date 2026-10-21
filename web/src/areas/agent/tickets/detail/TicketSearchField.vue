<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/** Ticket picker by ref (`WEB-12`), `#number` or subject (link and merge dialogs, FE-055). */
import { searchTickets } from './ticketApi'
import type { Rule } from '@/constants/validations'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ modelValue: S['TicketSummary'] | null; label: string; rules?: Rule[]; excludeId?: string; testid?: string }>()
const emit = defineEmits<{ 'update:modelValue': [S['TicketSummary'] | null] }>()
const title = (t: S['TicketSummary']) => `${t.ref ?? `#${t.number}`} · ${t.subject}`
async function fetchTickets(v: string): Promise<S['TicketSummary'][]> {
  if (!v) return []
  return (await searchTickets(v)).filter((t) => t.id !== props.excludeId)
}
</script>

<template>
  <AutocompleteSearch
    :model-value="modelValue"
    :fetch-items="fetchTickets"
    :initial-items="modelValue ? [modelValue] : []"
    :item-title="title"
    item-value="id"
    return-object
    :label="label"
    :rules="rules"
    :hint="$t('agent.ticket.links.searchHint')"
    persistent-hint
    :data-testid="testid ?? 'ticket-search'"
    @update:model-value="(v: S['TicketSummary'] | null) => emit('update:modelValue', v)"
  />
</template>
