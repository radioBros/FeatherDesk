<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Shows a secret that the server returns exactly once (API token, webhook
 * secret, inbound webhook URL). The parent renders it behind `v-if` from a
 * local ref and clears that ref on close, so the value leaves the DOM and the
 * store (FE-030, FE-040, FE-057).
 */
import CopyButton from './CopyButton.vue'

defineProps<{ value: string; label: string; warning?: string }>()
</script>

<template>
  <div data-testid="secret-once">
    <v-alert type="warning" variant="tonal" density="compact" class="mb-3">
      {{ warning ?? $t('agent.common.shownOnce') }}
    </v-alert>
    <v-text-field :model-value="value" :label="label" readonly hide-details class="st-mono" data-testid="secret-value">
      <template #append-inner><CopyButton :value="value" /></template>
    </v-text-field>
  </div>
</template>

<style scoped>
.st-mono :deep(input) { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
</style>
