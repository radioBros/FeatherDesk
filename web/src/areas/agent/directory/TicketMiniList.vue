<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Tickets of a customer or organization, through the normal visibility-scoped list API (FE-026). */
import { onMounted, watch } from 'vue'
import { api, unwrap } from '@/api/client'
import StatusChip from '@/components/StatusChip.vue'
import PriorityChip from '@/components/PriorityChip.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import TicketNumber from '@/components/TicketNumber.vue'
import ProjectChip from '@/components/ProjectChip.vue'
import EmptyState from '@/components/EmptyState.vue'
import { useI18n } from 'vue-i18n'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import { useCursorList } from '../shared/useCursorList'
import ColumnCells from '@/components/ColumnCells.vue'

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
const { t } = useI18n()
const cols = useTableColumns('ticket-mini', () => [
  { key: 'number', label: t('projects.ticket.ref'), locked: true },
  { key: 'project', label: t('projects.ticket.project'), lowPriority: true },
  { key: 'subject', label: t('agent.list.columns.subject'), locked: true },
  { key: 'status', label: t('agent.list.columns.status') },
  { key: 'priority', label: t('agent.list.columns.priority'), lowPriority: true },
  { key: 'updated', label: t('agent.list.columns.updated'), lowPriority: true },
])
</script>

<template>
  <div data-testid="ticket-mini-list">
    <v-progress-linear v-if="list.loading.value" indeterminate />
    <v-alert v-if="list.error.value" type="error" variant="tonal" density="compact" class="ma-2">{{ $t('agent.common.loadFailed') }}</v-alert>
    <EmptyState v-else-if="!list.loading.value && !list.items.value.length" :title="$t('agent.customers.noTickets')" body="" />
    <template v-else>
    <ColumnsMenu :cols="cols" />
    <v-table density="compact">
      <tbody>
        <tr v-for="tk in list.items.value" :key="tk.id" data-testid="mini-ticket-row">
          <ColumnCells :cols="cols">
            <td data-col="number" v-if="cols.show('number')"><router-link :to="`/agent/tickets/${tk.ref ?? tk.number}`" class="text-decoration-none"><TicketNumber :number="tk.number" :reftext="tk.ref" /></router-link></td>
            <td data-col="project" v-if="cols.show('project')"><ProjectChip :project="tk.project" /></td>
            <td data-col="subject" v-if="cols.show('subject')"><router-link :to="`/agent/tickets/${tk.ref ?? tk.number}`" class="text-decoration-none text-high-emphasis">{{ tk.subject }}</router-link></td>
            <td data-col="status" v-if="cols.show('status')"><StatusChip :status="tk.status" /></td>
            <td data-col="priority" v-if="cols.show('priority')"><PriorityChip :priority="tk.priority" /></td>
            <td data-col="updated" v-if="cols.show('updated')" class="text-medium-emphasis"><RelativeTime :value="tk.updated_at" /></td>
          </ColumnCells>
        </tr>
      </tbody>
    </v-table>
    </template>
    <div v-if="list.next.value" class="pa-2 text-center">
      <v-btn variant="text" :loading="list.loading.value" @click="list.loadMore()">{{ $t('agent.common.loadMore') }}</v-btn>
    </div>
  </div>
</template>
