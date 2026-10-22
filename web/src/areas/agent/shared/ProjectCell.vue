<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Project column of a configuration table: the chip of one project (`id`) or of
 * several (`ids`); no project means the row applies to every project.
 */
import { computed } from 'vue'
import ProjectChip from '@/components/ProjectChip.vue'
import { useProjectsStore } from '@/stores/projects'

const props = defineProps<{ id?: string | null; ids?: string[] | null; emptyLabel?: string }>()
const store = useProjectsStore()
const chips = computed(() => {
  const list = props.ids ?? (props.id ? [props.id] : [])
  return list.map((i) => store.byId(i) ?? { id: i, key: '?', name: i, color: '' })
})
</script>

<template>
  <span class="d-inline-flex flex-wrap ga-1" data-testid="project-cell">
    <ProjectChip v-for="p in chips" :key="p.id" :project="p" />
    <span v-if="!chips.length" class="text-medium-emphasis">{{ emptyLabel ?? $t('projects.config.all') }}</span>
  </span>
</template>
