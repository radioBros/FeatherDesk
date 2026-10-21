<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { mdiHammerWrench } from '@mdi/js'
import EmptyState from './EmptyState.vue'
</script>

<template>
  <v-container>
    <EmptyState :icon="mdiHammerWrench" :title="$route.name ? String($route.name) : ''" :body="$t('app.comingSoon')" />
  </v-container>
</template>
