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
  '--fd-sec': `var(--v-theme-nav-${props.section})`,
  '--fd-on': `var(--v-theme-on-nav-${props.section})`,
  width: `${props.size}px`,
  height: `${props.size}px`,
}))
</script>

<template>
  <span class="fd-sec-badge" :class="{ 'fd-sec-badge--active': active, 'fd-sec-badge--circle': circle }" :style="style" aria-hidden="true">
    <v-icon :icon="icon" :size="Math.round(size * 0.62)" />
  </span>
</template>

<style scoped>
.fd-sec-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: none;
  border-radius: 8px;
  background: rgba(var(--fd-sec), 0.16);
  color: rgb(var(--fd-sec));
  transition: background-color 0.15s, color 0.15s;
}
.fd-sec-badge--circle { border-radius: 50%; }
.fd-sec-badge--active { background: rgb(var(--fd-sec)); color: rgb(var(--fd-on)); }
</style>
