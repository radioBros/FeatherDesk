<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
defineProps<{ number: number | string }>()
</script>

<template>
  <span class="font-weight-medium text-medium-emphasis">#{{ number }}</span>
</template>
