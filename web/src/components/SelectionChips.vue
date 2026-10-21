<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * `#selection` slot content for a multiple select: the first value as a small chip, the rest as
 * "+N more", so a multiselect never grows taller than a text field (input density, FE-032).
 */
import { computed } from 'vue'

// `item` is the slot's list item (it carries `title`); typed loosely because Vuetify types it by the raw items.
const props = withDefaults(defineProps<{ item: unknown; index: number; total: number; max?: number }>(), { max: 1 })
const title = computed(() => {
  const i = props.item
  if (i && typeof i === 'object' && 'title' in i) return String((i as { title: unknown }).title)
  return String(i)
})
</script>

<template>
  <v-chip v-if="index < max" size="small" label class="st-sel-chip">{{ title }}</v-chip>
  <span v-else-if="index === max" class="text-caption text-medium-emphasis ms-1">{{ $t('app.moreCount', { n: total - max }) }}</span>
</template>

<style scoped>
.st-sel-chip { max-width: 100%; }
.st-sel-chip :deep(.v-chip__content) { overflow: hidden; text-overflow: ellipsis; }
</style>
