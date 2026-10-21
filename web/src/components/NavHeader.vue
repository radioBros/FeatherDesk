<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** A drawer section header with a small dot in the section colour. */
import type { Section } from '@/theme/palette'

defineProps<{ section: Section }>()
</script>

<template>
  <v-list-subheader class="st-nav-header" :style="{ '--st-sec': `var(--v-theme-nav-${section})` }">
    <span class="st-nav-dot" aria-hidden="true" /><slot />
  </v-list-subheader>
</template>

<style>
.st-nav-header .st-nav-dot {
  display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-inline-end: 8px; vertical-align: middle;
  background: rgb(var(--st-sec));
}
</style>
