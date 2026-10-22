<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed } from 'vue'
import { useSectionMeta } from '@/theme/useSectionMeta'
import type { Section } from '@/theme/palette'
import SectionIcon from './SectionIcon.vue'

/** Page title with a coloured badge of the page's section (from the route unless `section` and `icon` are given). */
const props = defineProps<{ title: string; subtitle?: string; section?: Section; icon?: string }>()
const fromRoute = useSectionMeta()
const meta = computed(() => (props.section && props.icon ? { section: props.section, icon: props.icon } : fromRoute.value))
</script>

<template>
  <div class="d-flex flex-wrap align-center ga-3 mb-4">
    <SectionIcon v-if="meta" :section="meta.section" :icon="meta.icon" :size="40" data-testid="page-badge" />
    <div class="flex-grow-1">
      <h1 class="text-h5">{{ title }}</h1>
      <div v-if="subtitle" class="text-body-2 text-medium-emphasis">{{ subtitle }}</div>
    </div>
    <slot name="actions" />
  </div>
</template>
