<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed } from 'vue'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()
const current = computed(() => ui.toasts[0] ?? null)
</script>

<template>
  <v-snackbar
    v-if="current"
    :key="current.id"
    :model-value="true"
    :color="current.color"
    :timeout="current.timeout"
    location="bottom right"
    @update:model-value="ui.dismiss(current.id)"
  >
    {{ current.text }}
    <template #actions>
      <v-btn variant="text" @click="ui.dismiss(current.id)">{{ $t('app.close') }}</v-btn>
    </template>
  </v-snackbar>
</template>
