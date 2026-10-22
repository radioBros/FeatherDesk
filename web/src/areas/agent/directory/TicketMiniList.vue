<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Tickets of a customer or organization, through the normal visibility-scoped list API (FE-026). */
import { onMounted, ref, watch } from 'vue'
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
import DataTable from '@/components/DataTable.vue'
import type { components } from '@/api/schema'

const props = defineProps<{ requesterId?: string; organizationId?: string }>()
// Header sorting is the API's `sort`; the list opens most recently updated first.
const DEFAULT_SORT = '-updated_at'
const sort = ref<components['schemas']['TicketSort']>(DEFAULT_SORT)
const list = useCursorList((cursor) =>
  unwrap(
    api.GET('/agent/tickets', {
      params: {
        query: {
          requester_id: props.requesterId,
          organization_id: props.organizationId,
          sort: sort.value,
          cursor: cursor ?? undefined,
          limit: 25,
        },
      },
    }),
  ),
)
onMounted(() => void list.reload())
watch(() => [props.requesterId, props.organizationId, sort.value], () => void list.reload())
const { t } = useI18n()
const cols = useTableColumns('ticket-mini', () => [
  { key: 'number', label: t('projects.ticket.ref'), locked: true, sort: { asc: 'number', desc: '-number' } },
  { key: 'project', label: t('projects.ticket.project'), lowPriority: true },
  { key: 'subject', label: t('agent.list.columns.subject'), locked: true },
  { key: 'status', label: t('agent.list.columns.status') },
  { key: 'priority', label: t('agent.list.columns.priority'), lowPriority: true, sort: { asc: 'priority', desc: '-priority' } },
  { key: 'updated', label: t('agent.list.columns.updated'), lowPriority: true, sort: { asc: 'updated_at', desc: '-updated_at' } },
])
</script>

<template>
  <div data-testid="ticket-mini-list">
    <v-progress-linear v-if="list.loading.value" indeterminate />
    <v-alert v-if="list.error.value" type="error" variant="tonal" density="compact" class="ma-2">{{ $t('agent.common.loadFailed') }}</v-alert>
    <EmptyState v-else-if="!list.loading.value && !list.items.value.length" :title="$t('agent.customers.noTickets')" body="" />
    <template v-else>
    <ColumnsMenu :cols="cols" />
    <DataTable
      :cols="cols"
      :items="list.items.value"
      :loading="list.loading.value"
      server-sort
      :sort="sort"
      :has-more="!!list.next.value"
      must-sort
      max-height="480px"
      density="compact"
      :row-props="{ 'data-testid': 'mini-ticket-row' }"
      @update:sort="(v: string | null) => (sort = (v ?? DEFAULT_SORT) as typeof sort)"
      @load-more="list.loadMore()"
    >
      <template #[`item.number`]="{ item }"><router-link :to="`/agent/tickets/${item.ref ?? item.number}`" class="text-decoration-none"><TicketNumber :number="item.number" :reftext="item.ref" /></router-link></template>
      <template #[`item.project`]="{ item }"><ProjectChip :project="item.project" /></template>
      <template #[`item.subject`]="{ item }"><router-link :to="`/agent/tickets/${item.ref ?? item.number}`" class="text-decoration-none text-high-emphasis">{{ item.subject }}</router-link></template>
      <template #[`item.status`]="{ item }"><StatusChip :status="item.status" /></template>
      <template #[`item.priority`]="{ item }"><PriorityChip :priority="item.priority" /></template>
      <template #[`item.updated`]="{ item }"><span class="text-medium-emphasis"><RelativeTime :value="item.updated_at" /></span></template>
    </DataTable>
    </template>
  </div>
</template>
