<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { mdiLockOutline } from '@mdi/js'
</script>

<template>
  <v-chip size="x-small" label color="secondary" :prepend-icon="mdiLockOutline" data-testid="env-lock">
    {{ $t('agent.common.setByEnv') }}
  </v-chip>
</template>
