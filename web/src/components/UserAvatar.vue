<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed } from 'vue'
import { avatarColor } from '@/theme/palette'

const props = withDefaults(defineProps<{ name?: string | null; size?: number | string; color?: string }>(), {
  size: 32,
  color: undefined,
})
const tone = computed(() => props.color ?? avatarColor(props.name))
const initials = computed(() => {
  const parts = (props.name ?? '').trim().split(/\s+/).filter(Boolean)
  if (!parts.length) return '?'
  const first = parts[0]!.charAt(0)
  const last = parts.length > 1 ? parts[parts.length - 1]!.charAt(0) : ''
  return (first + last).toUpperCase()
})
</script>

<template>
  <v-avatar :size="size" :color="tone" variant="tonal" :aria-label="name ?? $t('user.unknown')">
    <span class="text-caption font-weight-bold">{{ initials }}</span>
  </v-avatar>
</template>
