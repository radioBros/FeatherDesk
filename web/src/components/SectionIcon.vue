<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * A section's icon on a small tonal rounded square or circle in the section hue
 * (`theme/palette.ts`). `active` turns it solid. Decorative: always aria-hidden,
 * the label next to it carries the meaning.
 */
import { computed } from 'vue'
import type { Section } from '@/theme/palette'

const props = withDefaults(defineProps<{ section: Section; icon: string; size?: number; active?: boolean; circle?: boolean }>(), {
  size: 28,
  active: false,
  circle: false,
})
const style = computed(() => ({
  '--st-sec': `var(--v-theme-nav-${props.section})`,
  '--st-on': `var(--v-theme-on-nav-${props.section})`,
  width: `${props.size}px`,
  height: `${props.size}px`,
}))
</script>

<template>
  <span class="st-sec-badge" :class="{ 'st-sec-badge--active': active, 'st-sec-badge--circle': circle }" :style="style" aria-hidden="true">
    <v-icon :icon="icon" :size="Math.round(size * 0.62)" />
  </span>
</template>

<style scoped>
.st-sec-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: none;
  border-radius: 8px;
  background: rgba(var(--st-sec), 0.16);
  color: rgb(var(--st-sec));
  transition: background-color 0.15s, color 0.15s;
}
.st-sec-badge--circle { border-radius: 50%; }
.st-sec-badge--active { background: rgb(var(--st-sec)); color: rgb(var(--st-on)); }
</style>
