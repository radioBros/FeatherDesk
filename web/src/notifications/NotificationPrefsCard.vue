<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** The "Notifications" card of both profile pages (FE-095): passes the session's matrix to the form. */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useSessionStore } from '@/stores/session'
import { useUiStore } from '@/stores/ui'
import NotificationPrefsForm from './NotificationPrefsForm.vue'
import type { Area, Prefs } from './types'

defineProps<{ area: Area }>()
const { t } = useI18n()
const session = useSessionStore()
const ui = useUiStore()
const prefs = computed(() => (session.me as { notification_prefs?: Prefs } | null)?.notification_prefs)
</script>

<template>
  <v-card border flat :title="$t('notifications.prefs.title')" :subtitle="$t('notifications.prefs.subtitle')" class="mb-4" data-testid="notification-prefs-card">
    <v-card-text>
      <NotificationPrefsForm :key="session.me?.id" :area="area" :prefs="prefs" @saved="ui.notify(t('notifications.prefs.saved'), 'success')" />
    </v-card-text>
  </v-card>
</template>
