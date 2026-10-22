<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * The project chip (D-20): a colour dot and the key (`WEB`), next to a ticket
 * ref or in a table. The colour is the project's own (`#RRGGBB`, may be empty),
 * so the dot carries it and the text stays in the theme's text colour (always
 * readable). `name` shows as a tooltip; `showName` adds it to the chip.
 */
import { computed } from 'vue'
import { HUES } from '@/theme/palette'

const props = defineProps<{ project?: { key: string; name?: string; color?: string } | null; showName?: boolean; size?: 'x-small' | 'small' }>()
const dot = computed(() => props.project?.color || HUES.brown.light)
</script>

<template>
  <v-chip
    v-if="project"
    :size="size ?? 'x-small'"
    variant="outlined"
    label
    class="fd-project-chip"
    :title="project.name"
    data-testid="project-chip"
  >
    <span class="fd-project-dot" :style="{ background: dot }" aria-hidden="true" />
    <span class="font-weight-medium">{{ project.key }}</span>
    <span v-if="showName && project.name" class="ml-1 text-medium-emphasis">{{ project.name }}</span>
  </v-chip>
</template>

<style scoped>
.fd-project-dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-inline-end: 6px; flex: none; }
</style>
