<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed } from 'vue'
import { mdiInboxOutline } from '@mdi/js'
import { useSectionMeta } from '@/theme/useSectionMeta'
import type { Section } from '@/theme/palette'
import SectionIcon from './SectionIcon.vue'

/** An empty list: a coloured icon badge (the page's section hue unless `section` is given), a title and a hint. */
const props = withDefaults(defineProps<{ title?: string; body?: string; icon?: string; section?: Section }>(), { icon: mdiInboxOutline })
const fromRoute = useSectionMeta()
const tone = computed<Section>(() => props.section ?? fromRoute.value?.section ?? 'tickets')
</script>

<template>
  <div class="d-flex flex-column align-center text-center pa-8 text-medium-emphasis" data-testid="empty-state">
    <SectionIcon :section="tone" :icon="icon" :size="72" circle class="mb-3" />
    <div class="text-h6 mb-1">{{ title ?? $t('empty.title') }}</div>
    <div class="text-body-2 mb-4">{{ body ?? $t('empty.body') }}</div>
    <slot />
  </div>
</template>
