<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed } from 'vue'
import { useRealtimeStore } from '@/stores/realtime'

const rt = useRealtimeStore()
// Only after a first successful connection: a never-connected socket is not "lost".
const lost = computed(() => rt.client !== null && rt.everConnected && !rt.connected)
</script>

<template>
  <v-system-bar v-if="lost" color="warning" data-testid="connection-lost" role="status">
    {{ $t('app.offline') }}
  </v-system-bar>
</template>
