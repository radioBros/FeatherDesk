<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Delivery log of one webhook (FE-040): status, latency, error, attempt; cursor "Load more". */
import { onMounted } from 'vue'
import { api, unwrap } from '@/api/client'
import RelativeTime from '@/components/RelativeTime.vue'
import EmptyState from '@/components/EmptyState.vue'
import { useCursorList } from '../shared/useCursorList'

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
</script>

<template>
  <div data-testid="webhook-deliveries">
    <v-progress-linear v-if="list.loading.value" indeterminate />
    <v-alert v-if="list.error.value" type="error" variant="tonal" density="compact">{{ $t('agent.common.loadFailed') }}</v-alert>
    <EmptyState v-if="!list.loading.value && !list.items.value.length" :title="$t('agent.webhooks.noDeliveries')" body="" />
    <v-table v-else density="compact">
      <thead>
        <tr>
          <th>{{ $t('agent.webhooks.when') }}</th>
          <th>{{ $t('agent.webhooks.event') }}</th>
          <th>{{ $t('agent.webhooks.statusCode') }}</th>
          <th>{{ $t('agent.webhooks.latency') }}</th>
          <th>{{ $t('agent.webhooks.attempt') }}</th>
          <th>{{ $t('agent.webhooks.error') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="d in list.items.value" :key="d.id" data-testid="delivery-row">
          <td><RelativeTime :value="d.created_at" /></td>
          <td class="text-caption">{{ d.event_type ?? '—' }}</td>
          <td>
            <v-chip size="x-small" label :color="ok(d.status_code) ? 'success' : 'error'">{{ d.status_code ?? '—' }}</v-chip>
          </td>
          <td>{{ d.response_ms != null ? `${d.response_ms} ms` : '—' }}</td>
          <td>{{ d.attempt }}</td>
          <td class="text-caption">{{ d.error ?? '' }}</td>
        </tr>
      </tbody>
    </v-table>
    <div v-if="list.next.value" class="text-center mt-2">
      <v-btn variant="text" :loading="list.loading.value" @click="list.loadMore()">{{ $t('agent.common.loadMore') }}</v-btn>
    </div>
  </div>
</template>
