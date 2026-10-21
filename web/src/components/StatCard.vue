<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * A count or dashboard card: a tonal card in the section hue with an icon badge,
 * a label and the value (default slot). Other attributes (`to`, `data-testid`)
 * go to the card.
 */
import type { Section } from '@/theme/palette'
import SectionIcon from './SectionIcon.vue'

defineOptions({ inheritAttrs: false })
defineProps<{ section: Section; icon: string; label: string }>()
</script>

<template>
  <v-card v-bind="$attrs" flat class="st-stat pa-3" :style="{ '--st-sec': `var(--v-theme-nav-${section})` }">
    <div class="d-flex align-center ga-3">
      <SectionIcon :section="section" :icon="icon" :size="44" />
      <div class="min-width-0">
        <div class="text-caption text-medium-emphasis">{{ label }}</div>
        <slot />
      </div>
    </div>
    <slot name="footer" />
  </v-card>
</template>

<style scoped>
.st-stat {
  background: rgba(var(--st-sec), 0.1);
  border: 1px solid rgba(var(--st-sec), 0.35);
}
.min-width-0 { min-width: 0; }
</style>
