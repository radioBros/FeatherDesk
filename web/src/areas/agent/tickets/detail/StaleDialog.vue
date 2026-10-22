<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** 412 (FE-045): someone else changed the ticket; reload keeps the composer draft. */
defineProps<{ open: boolean }>()
const emit = defineEmits<{ reload: []; close: [] }>()
</script>

<template>
  <v-dialog :model-value="open" max-width="440" persistent>
    <v-card :title="$t('agent.ticket.stale.title')" data-testid="stale-dialog">
      <v-card-text>{{ $t('agent.ticket.stale.body') }}</v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn variant="text" @click="emit('close')">{{ $t('app.close') }}</v-btn>
        <v-btn color="primary" data-testid="stale-reload" @click="emit('reload')">{{ $t('agent.ticket.stale.reload') }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>
