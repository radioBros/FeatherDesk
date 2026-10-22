<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { confirmState, settleConfirm } from '@/composables/useConfirm'

const state = confirmState()
</script>

<template>
  <v-dialog :model-value="state.open" max-width="440" @update:model-value="(v: boolean) => !v && settleConfirm(false)">
    <v-card data-testid="confirm-dialog">
      <v-card-title>{{ state.options.title ?? $t('confirm.title') }}</v-card-title>
      <v-card-text>{{ state.options.body ?? $t('confirm.body') }}</v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn variant="text" data-testid="confirm-cancel" @click="settleConfirm(false)">
          {{ state.options.cancelText ?? $t('app.cancel') }}
        </v-btn>
        <v-btn :color="state.options.color ?? 'primary'" variant="flat" data-testid="confirm-ok" @click="settleConfirm(true)">
          {{ state.options.confirmText ?? $t('app.confirm') }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>
