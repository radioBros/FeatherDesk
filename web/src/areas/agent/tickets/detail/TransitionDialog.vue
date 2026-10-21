<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** 409 `invalid-transition` (FE-022/023): shows the statuses the ticket can move to. */
import type { TransitionConflict } from './useTicketDetail'

defineProps<{ conflict: TransitionConflict | null }>()
</script>

<template>
  <v-dialog :model-value="!!conflict" max-width="460" persistent>
    <v-card v-if="conflict" :title="$t('agent.ticket.transition.title')" data-testid="transition-dialog">
      <v-card-text>
        <p class="mb-3">{{ $t('agent.ticket.transition.body') }}</p>
        <div class="d-flex flex-wrap ga-2">
          <v-btn
            v-for="s in conflict.allowed"
            :key="s"
            variant="tonal"
            color="primary"
            :data-testid="`transition-${s}`"
            @click="conflict.pick(s)"
          >
            {{ $t(`status.${s}`) }}
          </v-btn>
        </div>
        <p v-if="!conflict.allowed.length" class="text-medium-emphasis">{{ $t('agent.ticket.transition.none') }}</p>
      </v-card-text>
      <v-card-actions>
        <v-btn v-if="conflict.skip" variant="text" data-testid="transition-skip" @click="conflict.skip()">
          {{ $t('agent.ticket.transition.withoutStatus') }}
        </v-btn>
        <v-spacer />
        <v-btn variant="text" data-testid="transition-cancel" @click="conflict.pick(null)">{{ $t('app.cancel') }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>
