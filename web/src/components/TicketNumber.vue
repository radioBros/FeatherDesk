<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** The ticket's identifier: its project ref (`WEB-123`) when known, else `#number`. */
defineProps<{ number: number | string; reftext?: string | null }>()
</script>

<template>
  <span class="font-weight-medium text-medium-emphasis" data-testid="ticket-ref">{{ reftext || `#${number}` }}</span>
</template>
