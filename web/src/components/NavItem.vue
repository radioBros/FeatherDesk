<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * A drawer or menu entry: a `v-list-item` whose icon sits on a tonal badge in the
 * section colour; the active entry gets a stronger tint and a solid badge. All
 * other attributes (`to`, `title`, `data-testid`, …) go to the list item.
 */
import { computed } from 'vue'
import type { Section } from '@/theme/palette'
import SectionIcon from './SectionIcon.vue'

defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{ section: Section; icon: string; size?: number }>(), { size: 28 })
const style = computed(() => ({ '--fd-sec': `var(--v-theme-nav-${props.section})` }))
</script>

<template>
  <v-list-item v-bind="$attrs" class="fd-nav-item" :style="style">
    <template #prepend="{ isActive }"><SectionIcon :section="section" :icon="icon" :size="size" :active="!!isActive" /></template>
    <template v-if="$slots.append" #append><slot name="append" /></template>
  </v-list-item>
</template>

<style>
.fd-nav-item.v-list-item--active { background: rgba(var(--fd-sec), 0.14); }
.fd-nav-item .v-list-item__spacer { width: 12px !important; }
.fd-nav-item.v-list-item--active .v-list-item-title { font-weight: 600; }
</style>
