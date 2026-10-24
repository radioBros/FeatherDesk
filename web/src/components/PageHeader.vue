<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed } from 'vue'
import { useDisplay } from 'vuetify'
import { useSectionMeta } from '@/theme/useSectionMeta'
import type { Section } from '@/theme/palette'
import SectionIcon from './SectionIcon.vue'

/**
 * Page title with a coloured badge of the page's section (from the route unless `section` and `icon` are given).
 * The badge and title share one row at every width (the title wraps, it never pushes the badge onto a row of its
 * own); on a phone both are smaller and the actions take a full row below.
 */
const props = defineProps<{ title: string; subtitle?: string; section?: Section; icon?: string }>()
const fromRoute = useSectionMeta()
const meta = computed(() => (props.section && props.icon ? { section: props.section, icon: props.icon } : fromRoute.value))
const { xs } = useDisplay()
</script>

<template>
  <div class="fd-page-header mb-4">
    <div class="d-flex align-center ga-3 fd-page-header__main">
      <SectionIcon v-if="meta" :section="meta.section" :icon="meta.icon" :size="xs ? 32 : 40" data-testid="page-badge" />
      <div class="fd-page-header__text">
        <h1 :class="xs ? 'text-h6' : 'text-h5'">{{ title }}</h1>
        <div v-if="subtitle" class="text-body-2 text-medium-emphasis">{{ subtitle }}</div>
      </div>
    </div>
    <div v-if="$slots.actions" class="fd-page-header__actions"><slot name="actions" /></div>
  </div>
</template>

<style scoped>
.fd-page-header { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; }
.fd-page-header__main { flex: 1 1 320px; min-width: 0; }
.fd-page-header__text { flex: 1 1 0; min-width: 0; overflow-wrap: anywhere; }
.fd-page-header__text h1 { line-height: 1.25; }
.fd-page-header__actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
@media (max-width: 599.98px) {
  .fd-page-header { gap: 8px; }
  .fd-page-header__actions { flex: 1 1 100%; }
}
</style>
