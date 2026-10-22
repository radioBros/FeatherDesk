<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Write-only secret input (SMTP / IMAP password, FE-048, FE-057): never
 * pre-filled; shows a "password set" chip; the parent sends the value only
 * when it is non-empty (i.e. changed).
 */
import PasswordField from '@/components/PasswordField.vue'
import EnvLockBadge from './EnvLockBadge.vue'

defineProps<{ modelValue: string; label: string; passwordSet?: boolean; locked?: boolean; testid?: string }>()
const emit = defineEmits<{ 'update:modelValue': [string] }>()
</script>

<template>
  <PasswordField
    :model-value="modelValue"
    :label="label"
    autocomplete="new-password"
    :disabled="locked"
    :placeholder="passwordSet ? $t('agent.common.passwordKeep') : ''"
    persistent-placeholder
    :data-testid="testid ?? 'password-write-only'"
    @update:model-value="(v: string) => emit('update:modelValue', v)"
  >
    <template #append-inner>
      <EnvLockBadge v-if="locked" />
      <v-chip v-else-if="passwordSet" size="x-small" label color="success" data-testid="password-set">
        {{ $t('agent.common.passwordSet') }}
      </v-chip>
    </template>
  </PasswordField>
</template>
