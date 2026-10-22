<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Delivery log of one webhook (FE-040): status, latency, error, attempt; cursor "Load more". */
import { onMounted } from 'vue'
import { api, unwrap } from '@/api/client'
import RelativeTime from '@/components/RelativeTime.vue'
import EmptyState from '@/components/EmptyState.vue'
import { useI18n } from 'vue-i18n'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import { useCursorList } from '../shared/useCursorList'
import ColumnCells from '@/components/ColumnCells.vue'

const props = defineProps<{ webhookId: string }>()
const list = useCursorList((cursor) =>
  unwrap(
    api.GET('/admin/webhooks/{id}/deliveries', {
      params: { path: { id: props.webhookId }, query: cursor ? { cursor, limit: 50 } : { limit: 50 } },
    }),
  ),
)
onMounted(() => void list.reload())
const ok = (code?: number | null) => !!code && code >= 200 && code < 300
const { t } = useI18n()
const cols = useTableColumns('webhook-deliveries', () => [
  { key: 'when', label: t('agent.webhooks.when'), locked: true },
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
    <v-table density="compact">
      <thead>
        <tr>
          <ColumnCells :cols="cols" head>
            <th data-col="when" v-if="cols.show('when')">{{ $t('agent.webhooks.when') }}</th>
            <th data-col="event" v-if="cols.show('event')">{{ $t('agent.webhooks.event') }}</th>
            <th data-col="statusCode" v-if="cols.show('statusCode')">{{ $t('agent.webhooks.statusCode') }}</th>
            <th data-col="latency" v-if="cols.show('latency')">{{ $t('agent.webhooks.latency') }}</th>
            <th data-col="attempt" v-if="cols.show('attempt')">{{ $t('agent.webhooks.attempt') }}</th>
            <th data-col="error" v-if="cols.show('error')">{{ $t('agent.webhooks.error') }}</th>
          </ColumnCells>
        </tr>
      </thead>
      <tbody>
        <tr v-for="d in list.items.value" :key="d.id" data-testid="delivery-row">
          <ColumnCells :cols="cols">
            <td data-col="when" v-if="cols.show('when')"><RelativeTime :value="d.created_at" /></td>
            <td data-col="event" v-if="cols.show('event')" class="text-caption">{{ d.event_type ?? '—' }}</td>
            <td data-col="statusCode" v-if="cols.show('statusCode')">
              <v-chip size="x-small" label :color="ok(d.status_code) ? 'success' : 'error'">{{ d.status_code ?? '—' }}</v-chip>
            </td>
            <td data-col="latency" v-if="cols.show('latency')">{{ d.response_ms != null ? `${d.response_ms} ms` : '—' }}</td>
            <td data-col="attempt" v-if="cols.show('attempt')">{{ d.attempt }}</td>
            <td data-col="error" v-if="cols.show('error')" class="text-caption">{{ d.error ?? '' }}</td>
          </ColumnCells>
        </tr>
      </tbody>
    </v-table>
    </template>
    <div v-if="list.next.value" class="text-center mt-2">
      <v-btn variant="text" :loading="list.loading.value" @click="list.loadMore()">{{ $t('agent.common.loadMore') }}</v-btn>
    </div>
  </div>
</template>
