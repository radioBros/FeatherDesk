<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** One notification row (bell list and the notifications page, FE-091/FE-092). */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import RelativeTime from '@/components/RelativeTime.vue'
import ProjectChip from '@/components/ProjectChip.vue'
import { useConfigStore } from '@/stores/config'
import { detailOf, titleOf, type Notification } from './types'
import { iconFor, sectionFor } from './icons'
import SectionIcon from '@/components/SectionIcon.vue'

/** `showProject`: the project chip (D-20), hidden while the user works with a single project (`useShowProjectChip`). */
const props = defineProps<{ n: Notification; showProject?: boolean }>()
defineEmits<{ open: [n: Notification] }>()
const { t } = useI18n()
const config = useConfigStore()
const title = computed(() => titleOf(props.n, t, config.tierLabel))
const detail = computed(() => detailOf(props.n, t))
const unread = computed(() => !props.n.read_at)
</script>

<template>
  <v-list-item
    :class="{ 'st-unread': unread }"
    lines="two"
    data-testid="notification-item"
    :data-unread="unread ? 'true' : 'false'"
    @click="$emit('open', n)"
  >
    <template #prepend><SectionIcon :section="sectionFor(n.type)" :icon="iconFor(n.type)" :size="34" :active="false" circle /></template>
    <v-list-item-title class="text-wrap" :class="{ 'font-weight-bold': unread }">{{ title }}</v-list-item-title>
    <v-list-item-subtitle class="st-sub">
      <ProjectChip v-if="showProject" :project="n.project" class="mr-1" />
      <span v-if="detail">{{ detail }} · </span><RelativeTime :value="n.created_at" />
    </v-list-item-subtitle>
    <template #append>
      <span v-if="unread" class="st-dot" role="img" :aria-label="$t('notifications.page.unread')" />
      <slot name="actions" />
    </template>
  </v-list-item>
</template>

<style scoped>
.st-dot { display: inline-block; width: 10px; height: 10px; border-radius: 50%; background: rgb(var(--v-theme-nav-notifications)); margin-inline: 8px; }
.st-unread { background: rgba(var(--v-theme-nav-notifications), 0.08); }
/* Full-strength secondary line: the faded default fails WCAG contrast on the tinted unread rows (axe). */
.st-sub { opacity: 1; }
</style>
