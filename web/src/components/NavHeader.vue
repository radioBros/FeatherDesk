<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** A drawer section header with a small dot in the section colour. */
import type { Section } from '@/theme/palette'

defineProps<{ section: Section }>()
</script>

<template>
  <v-list-subheader class="fd-nav-header" :style="{ '--fd-sec': `var(--v-theme-nav-${section})` }">
    <span class="fd-nav-dot" aria-hidden="true" /><slot />
  </v-list-subheader>
</template>

<style>
.fd-nav-header .fd-nav-dot {
  display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-inline-end: 8px; vertical-align: middle;
  background: rgb(var(--fd-sec));
}
</style>
