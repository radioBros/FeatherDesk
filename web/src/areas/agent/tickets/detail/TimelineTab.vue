<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Timeline tab (FE-022): `/agent/tickets/{id}/events`, cursor paged; escalations show the reason. */
import { onMounted, watch } from 'vue'
import RelativeTime from '@/components/RelativeTime.vue'
import EmptyState from '@/components/EmptyState.vue'
import { useCursorList } from '../../shared/useCursorList'
import { events } from './ticketApi'
import { eventActor, eventDetail, eventLabel } from './events'

const props = defineProps<{ ticketId: string; refreshKey?: number }>()
const list = useCursorList((cursor) => events(props.ticketId, cursor))
onMounted(() => void list.reload())
watch(() => props.refreshKey, () => void list.reload())
</script>

<template>
  <div data-testid="timeline">
    <v-progress-linear v-if="list.loading.value && !list.items.value.length" indeterminate />
    <v-alert v-if="list.error.value" type="error" variant="tonal" density="compact">{{ $t('agent.common.loadFailed') }}</v-alert>
    <EmptyState v-if="!list.loading.value && !list.items.value.length && !list.error.value" :title="$t('agent.ticket.timeline.empty')" body="" />
    <v-timeline v-else density="compact" side="end" truncate-line="both">
      <v-timeline-item v-for="e in list.items.value" :key="e.id" size="x-small" dot-color="primary" data-testid="timeline-item">
        <div class="d-flex flex-wrap ga-2 align-baseline">
          <strong>{{ eventLabel(e.type) }}</strong>
          <span v-if="eventDetail(e)" class="text-body-2" data-testid="timeline-detail">{{ eventDetail(e) }}</span>
        </div>
        <div class="text-caption text-medium-emphasis">
          {{ eventActor(e) }} · <RelativeTime :value="e.created_at" />
        </div>
      </v-timeline-item>
    </v-timeline>
    <div v-if="list.next.value" class="text-center mt-2">
      <v-btn variant="text" :loading="list.loading.value" @click="list.loadMore()">{{ $t('agent.common.loadMore') }}</v-btn>
    </div>
  </div>
</template>
