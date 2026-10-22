<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** The bell's dropdown (FE-091): the latest entries, mark all read, a link to the full page. Lazy chunk. */
import { onMounted, ref } from 'vue'
import { mdiBellOutline, mdiCheckAll } from '@mdi/js'
import { useNotificationsStore } from './store'
import { centreFor, type Area, type Notification } from './types'
import { useShowProjectChip } from './projects'
import NotificationItem from './NotificationItem.vue'
import SectionIcon from '@/components/SectionIcon.vue'

const props = defineProps<{ area: Area }>()
const emit = defineEmits<{ close: [] }>()
const store = useNotificationsStore()
const loading = ref(!store.latestLoaded)
const failed = ref(false)
const showProject = useShowProjectChip(props.area, () => store.latest)

onMounted(async () => {
  try {
    await store.loadLatest()
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
})

function open(n: Notification) {
  emit('close')
  void store.open(n)
}
</script>

<template>
  <v-card min-width="320" max-width="400" data-testid="notification-menu">
    <div class="d-flex align-center ga-3 px-4 pt-3 pb-1">
      <SectionIcon section="notifications" :icon="mdiBellOutline" :size="32" circle />
      <div class="text-subtitle-1 flex-grow-1">{{ $t('notifications.page.title') }}</div>
      <v-btn
        size="small"
        variant="tonal"
        color="nav-notifications"
        :prepend-icon="mdiCheckAll"
        :disabled="store.unread === 0"
        data-testid="notification-read-all"
        @click="store.markAllRead()"
      >
        {{ $t('notifications.menu.markAllRead') }}
      </v-btn>
    </div>
    <v-progress-linear v-if="loading" indeterminate />
    <v-alert v-if="failed" type="warning" variant="tonal" density="compact" class="ma-3">{{ $t('notifications.menu.loadFailed') }}</v-alert>
    <v-list v-if="store.latest.length" density="compact" max-height="420" class="py-0" data-testid="notification-list">
      <NotificationItem v-for="n in store.latest" :key="n.id" :n="n" :show-project="showProject" @open="open" />
    </v-list>
    <div v-else-if="!loading && !failed" class="pa-6 text-center" data-testid="notification-empty">
      {{ $t('notifications.menu.empty') }}
    </div>
    <v-divider />
    <v-card-actions>
      <v-btn block variant="tonal" color="nav-notifications" :to="centreFor(props.area)" data-testid="notification-see-all" @click="emit('close')">
        {{ $t('notifications.menu.seeAll') }}
      </v-btn>
    </v-card-actions>
  </v-card>
</template>
