<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Tickets of a customer or organization, through the normal visibility-scoped list API (FE-026). */
import { onMounted, watch } from 'vue'
import { api, unwrap } from '@/api/client'
import StatusChip from '@/components/StatusChip.vue'
import PriorityChip from '@/components/PriorityChip.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import TicketNumber from '@/components/TicketNumber.vue'
import EmptyState from '@/components/EmptyState.vue'
import { useCursorList } from '../shared/useCursorList'

const props = defineProps<{ requesterId?: string; organizationId?: string }>()
const list = useCursorList((cursor) =>
  unwrap(
    api.GET('/agent/tickets', {
      params: {
        query: {
          requester_id: props.requesterId,
          organization_id: props.organizationId,
          sort: '-updated_at',
          cursor: cursor ?? undefined,
          limit: 25,
        },
      },
    }),
  ),
)
onMounted(() => void list.reload())
watch(() => [props.requesterId, props.organizationId], () => void list.reload())
</script>

<template>
  <div data-testid="ticket-mini-list">
    <v-progress-linear v-if="list.loading.value" indeterminate />
    <v-alert v-if="list.error.value" type="error" variant="tonal" density="compact" class="ma-2">{{ $t('agent.common.loadFailed') }}</v-alert>
    <EmptyState v-else-if="!list.loading.value && !list.items.value.length" :title="$t('agent.customers.noTickets')" body="" />
    <v-table v-else density="compact">
      <tbody>
        <tr v-for="tk in list.items.value" :key="tk.id" data-testid="mini-ticket-row">
          <td><router-link :to="`/agent/tickets/${tk.number}`" class="text-decoration-none"><TicketNumber :number="tk.number" /></router-link></td>
          <td><router-link :to="`/agent/tickets/${tk.number}`" class="text-decoration-none text-high-emphasis">{{ tk.subject }}</router-link></td>
          <td><StatusChip :status="tk.status" /></td>
          <td><PriorityChip :priority="tk.priority" /></td>
          <td class="text-medium-emphasis"><RelativeTime :value="tk.updated_at" /></td>
        </tr>
      </tbody>
    </v-table>
    <div v-if="list.next.value" class="pa-2 text-center">
      <v-btn variant="text" :loading="list.loading.value" @click="list.loadMore()">{{ $t('agent.common.loadMore') }}</v-btn>
    </div>
  </div>
</template>
