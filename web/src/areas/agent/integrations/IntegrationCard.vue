<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * One tile of the Integrations page (D-21): brand mark, name, status chip,
 * what it does, and the way in (its config view, or an external page).
 */
defineProps<{
  name: string
  description: string
  icon: string
  /** Brand colour of the mark (hex). */
  color: string
  status?: { text: string; color: string; icon?: string } | null
  detail?: string | null
  to?: string
  href?: string
  action: string
  testid: string
}>()
</script>

<template>
  <v-card
    border
    flat
    class="integration-card h-100 d-flex flex-column"
    :to="to"
    :href="href"
    :target="href ? '_blank' : undefined"
    :data-testid="testid"
  >
    <v-card-item class="pb-0">
      <template #prepend>
        <v-avatar rounded="lg" size="48" class="integration-mark" :style="{ '--mark': color }">
          <v-icon :icon="icon" size="28" :color="color" />
        </v-avatar>
      </template>
      <v-card-title class="text-subtitle-1 font-weight-medium">{{ name }}</v-card-title>
      <template #append>
        <v-chip v-if="status" size="small" label variant="tonal" :color="status.color" :prepend-icon="status.icon" data-testid="integration-status">
          {{ status.text }}
        </v-chip>
      </template>
    </v-card-item>
    <v-card-text class="flex-grow-1 text-body-2 text-medium-emphasis pt-3">{{ description }}</v-card-text>
    <v-divider />
    <v-card-actions class="px-4">
      <span v-if="detail" class="text-caption text-medium-emphasis text-truncate" data-testid="integration-detail">{{ detail }}</span>
      <v-spacer />
      <v-btn variant="tonal" color="primary" size="small" tabindex="-1">{{ action }}</v-btn>
    </v-card-actions>
  </v-card>
</template>

<style scoped>
.integration-mark {
  background: color-mix(in srgb, var(--mark) 12%, transparent);
}
.integration-card {
  transition: border-color 0.15s, box-shadow 0.15s;
}
.integration-card:hover {
  border-color: rgb(var(--v-theme-primary)) !important;
}
</style>
