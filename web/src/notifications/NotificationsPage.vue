<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * `/agent/notifications` and `/notifications` (D-19 §D, FE-092): every
 * notification of the caller, newest first, with All/Unread and type filters,
 * mark all read, per-row mark read and delete. New entries and reads from
 * other tabs arrive live through the notifications store (`me` topic).
 */
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { mdiBellOutline, mdiCheck, mdiCheckAll, mdiCogOutline, mdiDeleteOutline } from '@mdi/js'
import { api, unwrap } from '@/api/client'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import { useUiStore } from '@/stores/ui'
import { useNotificationsStore } from './store'
import NotificationItem from './NotificationItem.vue'
import { settingsFor, typeKey, typesFor, type Area, type Notification, type NotificationType } from './types'
import { useShowProjectChip } from './projects'

const { t } = useI18n()
const route = useRoute()
const ui = useUiStore()
const store = useNotificationsStore()
const area = computed<Area>(() => (route.meta.area === 'agent' ? 'agent' : 'portal'))

const show = ref<'all' | 'unread'>('all')
const type = ref<NotificationType | null>(null)
const items = ref<Notification[]>([])
const cursor = ref<string | null>(null)
const loading = ref(false)
const failed = ref(false)
const showProject = useShowProjectChip(area.value, () => items.value)
const typeItems = computed(() => typesFor(area.value).map((k) => ({ value: k, title: t(`notifications.types.${typeKey(k)}.label`) })))

async function load(more = false) {
  loading.value = true
  failed.value = false
  try {
    const page = await unwrap(
      api.GET('/me/notifications', {
        params: {
          query: {
            limit: 30,
            ...(show.value === 'unread' ? { unread: true } : {}),
            ...(type.value ? { type: type.value } : {}),
            ...(more && cursor.value ? { cursor: cursor.value } : {}),
          },
        },
      }),
    )
    items.value = more ? [...items.value, ...page.items] : page.items
    cursor.value = page.next_cursor
    store.unread = page.unread_count
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
watch([show, type], () => void load(), { immediate: true })

const offs = [
  store.onCreated((n) => {
    if (type.value && n.type !== type.value) return
    if (!items.value.some((x) => x.id === n.id)) items.value = [n, ...items.value]
  }),
  store.onRead((d) => {
    const at = new Date().toISOString()
    for (const n of items.value) if (!n.read_at && (d.all || d.ids?.includes(n.id))) n.read_at = at
  }),
]
onBeforeUnmount(() => offs.forEach((o) => o()))

async function markRead(n: Notification) {
  try {
    await store.markRead(n.id)
  } catch {
    ui.notify(t('notifications.menu.loadFailed'), 'error')
  }
}
async function markAll() {
  try {
    await store.markAllRead()
    ui.notify(t('notifications.page.allRead'), 'success')
  } catch {
    ui.notify(t('notifications.menu.loadFailed'), 'error')
  }
}
async function remove(n: Notification) {
  try {
    await store.remove(n.id)
    items.value = items.value.filter((x) => x.id !== n.id)
    ui.notify(t('notifications.page.deleted'), 'success')
  } catch {
    ui.notify(t('notifications.menu.loadFailed'), 'error')
  }
}
</script>

<template>
  <v-container class="py-6" style="max-width: 880px" data-testid="notifications-page">
    <PageHeader :title="$t('notifications.page.title')" :subtitle="$t('notifications.page.subtitle')">
      <template #actions>
        <v-btn variant="text" :prepend-icon="mdiCogOutline" :to="settingsFor(area)" data-testid="notifications-settings">{{ $t('notifications.page.settings') }}</v-btn>
        <v-btn color="primary" variant="tonal" :prepend-icon="mdiCheckAll" :disabled="store.unread === 0" data-testid="notifications-read-all" @click="markAll">
          {{ $t('notifications.menu.markAllRead') }}
        </v-btn>
      </template>
    </PageHeader>
    <div class="d-flex flex-wrap align-center ga-4 mb-4">
      <v-btn-toggle v-model="show" mandatory density="comfortable" variant="outlined" divided :aria-label="$t('notifications.page.filter')" data-testid="notifications-filter">
        <v-btn value="all" data-testid="notifications-filter-all">{{ $t('notifications.page.filterAll') }}</v-btn>
        <v-btn value="unread" data-testid="notifications-filter-unread">{{ $t('notifications.page.filterUnread') }}</v-btn>
      </v-btn-toggle>
      <v-select
        v-model="type"
        :items="typeItems"
        :label="$t('notifications.page.type')"
        :placeholder="$t('notifications.page.allTypes')"
        clearable
        hide-details
        density="compact"
        style="max-width: 320px"
        data-testid="notifications-type"
      />
    </div>
    <v-alert v-if="failed" type="warning" variant="tonal" density="compact" class="mb-3">{{ $t('notifications.menu.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="loading && !items.length" indeterminate />
      <v-list v-if="items.length" class="py-0" data-testid="notifications-list">
        <NotificationItem v-for="n in items" :key="n.id" :n="n" :show-project="showProject" @open="store.open(n)">
          <template #actions>
            <v-btn
              v-if="!n.read_at"
              :icon="mdiCheck"
              size="small"
              variant="text"
              :aria-label="$t('notifications.page.markRead')"
              data-testid="notification-mark-read"
              @click.stop="markRead(n)"
            />
            <v-btn
              :icon="mdiDeleteOutline"
              size="small"
              variant="text"
              :aria-label="$t('notifications.page.delete')"
              data-testid="notification-delete"
              @click.stop="remove(n)"
            />
          </template>
        </NotificationItem>
      </v-list>
      <EmptyState
        v-else-if="!loading"
        :icon="mdiBellOutline"
        :title="show === 'unread' ? $t('notifications.page.emptyUnread') : $t('notifications.page.empty')"
        body=""
      />
    </v-card>
    <div v-if="cursor" class="d-flex justify-center mt-4">
      <v-btn variant="text" :loading="loading" data-testid="notifications-more" @click="load(true)">{{ $t('notifications.page.loadMore') }}</v-btn>
    </div>
  </v-container>
</template>
