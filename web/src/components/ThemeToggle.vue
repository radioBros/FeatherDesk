<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { mdiThemeLightDark } from '@mdi/js'
import { useThemeMode } from '@/composables/useAppPrefs'
import NavItem from './NavItem.vue'

const { modes, set } = useThemeMode()
</script>

<template>
  <v-menu>
    <template #activator="{ props }">
      <v-btn v-bind="props" :icon="mdiThemeLightDark" variant="text" color="nav-branding" :aria-label="$t('app.theme')" />
    </template>
    <v-list density="compact">
      <NavItem v-for="m in modes" :key="m.value" :icon="m.icon" section="branding" :size="26" :title="$t(m.label)" @click="set(m.value)" />
    </v-list>
  </v-menu>
</template>
