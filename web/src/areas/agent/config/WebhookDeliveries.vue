<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Delivery log of one webhook (FE-040): status, latency, error, attempt; cursor "Load more", sortable by time. */
import { onMounted, ref, watch } from 'vue'
import { api, unwrap } from '@/api/client'
import RelativeTime from '@/components/RelativeTime.vue'
import EmptyState from '@/components/EmptyState.vue'
import { useI18n } from 'vue-i18n'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import { useCursorList } from '../shared/useCursorList'
import DataTable from '@/components/DataTable.vue'
import type { operations } from '@/api/schema'

type DeliveriesQuery = NonNullable<operations['listWebhookDeliveries']['parameters']['query']>
const props = defineProps<{ webhookId: string }>()
/** null = the endpoint's default order (newest first). */
const sort = ref<NonNullable<DeliveriesQuery['sort']> | null>(null)
const list = useCursorList((cursor) =>
  unwrap(
    api.GET('/admin/webhooks/{id}/deliveries', {
      params: { path: { id: props.webhookId }, query: { sort: sort.value ?? undefined, cursor: cursor ?? undefined, limit: 50 } },
    }),
  ),
)
onMounted(() => void list.reload())
watch(sort, () => void list.reload())
const ok = (code?: number | null) => !!code && code >= 200 && code < 300
const { t } = useI18n()
const cols = useTableColumns('webhook-deliveries', () => [
  { key: 'when', label: t('agent.webhooks.when'), locked: true, sort: { asc: 'created_at', desc: '-created_at' } },
  { key: 'event', label: t('agent.webhooks.event') },
  { key: 'statusCode', label: t('agent.webhooks.statusCode') },
  { key: 'latency', label: t('agent.webhooks.latency'), lowPriority: true },
  { key: 'attempt', label: t('agent.webhooks.attempt'), lowPriority: true },
  { key: 'error', label: t('agent.webhooks.error'), lowPriority: true },
])
</script>

<template>
  <div data-testid="webhook-deliveries">
    <v-progress-linear v-if="list.loading.value" indeterminate />
    <v-alert v-if="list.error.value" type="error" variant="tonal" density="compact">{{ $t('agent.common.loadFailed') }}</v-alert>
    <EmptyState v-if="!list.loading.value && !list.items.value.length" :title="$t('agent.webhooks.noDeliveries')" body="" />
    <template v-else>
    <ColumnsMenu :cols="cols" />
    <DataTable
      :cols="cols"
      :items="list.items.value"
      :loading="list.loading.value"
      server-sort
      :sort="sort"
      :has-more="!!list.next.value"
      density="compact"
      max-height="max(240px, calc(100dvh - 360px))"
      :row-props="{ 'data-testid': 'delivery-row' }"
      @update:sort="(v: string | null) => (sort = v as typeof sort)"
      @load-more="list.loadMore()"
    >
      <template #[`item.when`]="{ item }"><RelativeTime :value="item.created_at" /></template>
      <template #[`item.event`]="{ item }"><span class="text-caption">{{ item.event_type ?? '—' }}</span></template>
      <template #[`item.statusCode`]="{ item }">
        <v-chip size="x-small" label :color="ok(item.status_code) ? 'success' : 'error'">{{ item.status_code ?? '—' }}</v-chip>
      </template>
      <template #[`item.latency`]="{ item }">{{ item.response_ms != null ? `${item.response_ms} ms` : '—' }}</template>
      <template #[`item.attempt`]="{ item }">{{ item.attempt }}</template>
      <template #[`item.error`]="{ item }"><span class="text-caption">{{ item.error ?? '' }}</span></template>
    </DataTable>
    </template>
  </div>
</template>
