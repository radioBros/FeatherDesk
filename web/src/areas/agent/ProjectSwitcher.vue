<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * The agent top-bar project switcher (D-20): "All projects" plus the user's
 * active projects, each with its colour dot and key. The choice is saved on the
 * server (`PATCH /me current_project_id`) and filters every list, count and
 * report. Hidden while the user works in a single active project. The button is
 * a 40 px target like every other top-bar action (see AppBar).
 */
import { computed } from 'vue'
import { mdiChevronDown, mdiFolderMultipleOutline } from '@mdi/js'
import { useI18n } from 'vue-i18n'
import { useProjectsStore } from '@/stores/projects'
import { useUiStore } from '@/stores/ui'
import ProjectChip from '@/components/ProjectChip.vue'

const projects = useProjectsStore()
const ui = useUiStore()
const { t } = useI18n()
const label = computed(() => projects.current?.key)

async function pick(id: string | null) {
  try {
    await projects.select(id)
  } catch {
    ui.notify(t('projects.switcher.error'), 'error')
  }
}
</script>

<template>
  <v-menu v-if="projects.showSwitcher" location="bottom end">
    <template #activator="{ props: p }">
      <v-btn
        v-bind="p"
        variant="text"
        color="nav-projects"
        :aria-label="$t('projects.switcher.label')"
        :prepend-icon="mdiFolderMultipleOutline"
        :append-icon="mdiChevronDown"
        class="fd-project-switcher"
        data-testid="project-switcher"
      >
        <span class="fd-switcher-text">{{ label ?? $t('projects.switcher.all') }}</span>
      </v-btn>
    </template>
    <v-list density="compact" :aria-label="$t('projects.switcher.label')" data-testid="project-switcher-list">
      <v-list-item
        :active="projects.currentId === null"
        :title="$t('projects.switcher.all')"
        data-testid="project-option-all"
        @click="pick(null)"
      />
      <v-list-item
        v-for="p in projects.active"
        :key="p.id"
        :active="projects.currentId === p.id"
        :data-testid="`project-option-${p.key}`"
        @click="pick(p.id)"
      >
        <template #prepend><ProjectChip :project="p" size="small" class="mr-2" /></template>
        <v-list-item-title>{{ p.name }}</v-list-item-title>
      </v-list-item>
    </v-list>
  </v-menu>
</template>

<style scoped>
/* On a phone only the icons show, keeping the bar inside the viewport (topbar spacing rules). */
@media (max-width: 599.98px) {
  .fd-switcher-text { display: none; }
  .fd-project-switcher { min-width: 40px; padding-inline: 8px; }
}
</style>
