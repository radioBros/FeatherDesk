<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * The bell in the app bar of both areas (D-19 §D, FE-090): unread badge kept
 * live by the `me` topic; the dropdown list is a lazy chunk loaded on open.
 */
import { computed, defineAsyncComponent, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { mdiBellOutline } from '@mdi/js'
import { useSessionStore } from '@/stores/session'
import { useRealtimeStore } from '@/stores/realtime'
import { useNotificationsStore } from './store'
import type { Area } from './types'

const props = defineProps<{ area: Area }>()
const NotificationMenu = defineAsyncComponent(() => import('./NotificationMenu.vue'))
const { t } = useI18n()
const router = useRouter()
const session = useSessionStore()
const rt = useRealtimeStore()
const store = useNotificationsStore()
const open = ref(false)

// Bind to the signed-in user and the tab's socket; rebind on a new user or client.
watch(
  () => [session.me?.id, rt.client] as const,
  ([user]) => {
    if (user) store.start({ area: props.area, user, navigate: (path) => void router.push(path) })
    else store.stop()
  },
  { immediate: true },
)

const badge = computed(() => (store.unread > 99 ? '99+' : String(store.unread)))
const label = computed(() => (store.unread ? t('notifications.bell.labelUnread', { n: store.unread }) : t('notifications.bell.label')))
</script>

<template>
  <v-menu v-model="open" location="bottom end" :close-on-content-click="false">
    <template #activator="{ props: p }">
      <v-btn v-bind="p" icon variant="text" color="nav-notifications" :aria-label="label" data-testid="notification-bell">
        <v-badge :model-value="store.unread > 0" color="error" data-testid="notification-badge">
          <template #badge><span data-testid="notification-count">{{ badge }}</span></template>
          <v-icon :icon="mdiBellOutline" />
        </v-badge>
      </v-btn>
    </template>
    <NotificationMenu v-if="open" :area="area" @close="open = false" />
  </v-menu>
</template>
