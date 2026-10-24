<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { mdiTranslate } from '@mdi/js'
import { useDisplay } from 'vuetify'
import { useLocalePicker } from '@/composables/useAppPrefs'

const { locale, available, names, pick } = useLocalePicker()
const { xs } = useDisplay()
</script>

<template>
  <v-menu>
    <template #activator="{ props }">
      <v-btn
        v-if="xs"
        v-bind="props"
        :icon="mdiTranslate"
        variant="text"
        color="nav-email"
        data-intent="plain"
        :aria-label="$t('app.language')"
      />
      <v-btn
        v-else
        v-bind="props"
        :prepend-icon="mdiTranslate"
        variant="text"
        color="nav-email"
        data-intent="plain"
        :aria-label="$t('app.language')"
      >
        {{ locale.toUpperCase() }}
      </v-btn>
    </template>
    <v-list density="compact">
      <v-list-item v-for="l in available" :key="l" :title="names[l]" :active="l === locale" @click="pick(l)" />
    </v-list>
  </v-menu>
</template>
