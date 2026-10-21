<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Ticket picker by `#number` or subject (link and merge dialogs, FE-055). */
import { onBeforeUnmount, ref, watch } from 'vue'
import { searchTickets } from './ticketApi'
import type { Rule } from '@/constants/validations'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ modelValue: S['TicketSummary'] | null; label: string; rules?: Rule[]; excludeId?: string; testid?: string }>()
const emit = defineEmits<{ 'update:modelValue': [S['TicketSummary'] | null] }>()
const q = ref('')
const items = ref<S['TicketSummary'][]>([])
const loading = ref(false)
let timer: ReturnType<typeof setTimeout> | null = null
const title = (t: S['TicketSummary']) => `#${t.number} · ${t.subject}`

watch(q, (v) => {
  if (timer) clearTimeout(timer)
  // Selecting an item writes its title into the search text: that is not a query.
  if (!v || (props.modelValue && (v === props.modelValue.subject || v === title(props.modelValue)))) return
  timer = setTimeout(async () => {
    loading.value = true
    try {
      items.value = (await searchTickets(v)).filter((t) => t.id !== props.excludeId)
    } catch {
      items.value = []
    } finally {
      loading.value = false
    }
  }, 300)
})
// A pending search must not fire after the dialog closed.
onBeforeUnmount(() => {
  if (timer) clearTimeout(timer)
})
</script>

<template>
  <v-autocomplete
    :model-value="modelValue"
    v-model:search="q"
    :items="modelValue && !items.some((i) => i.id === modelValue!.id) ? [modelValue, ...items] : items"
    :item-title="title"
    item-value="id"
    return-object
    no-filter
    :loading="loading"
    :label="label"
    :rules="rules"
    :hint="$t('agent.ticket.links.searchHint')"
    persistent-hint
    :data-testid="testid ?? 'ticket-search'"
    @update:model-value="(v: S['TicketSummary'] | null) => emit('update:modelValue', v)"
  />
</template>
