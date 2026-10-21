<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed } from 'vue'
import { useConfigStore } from '@/stores/config'

const TIER_COLOR = ['nav-teams', 'nav-customers', 'nav-branding']

const props = withDefaults(defineProps<{ tier: number; size?: string }>(), { size: 'small' })
const config = useConfigStore()
// Tier labels are instance settings (e.g. "Front desk / Specialist / Engineering", D-05).
const label = computed(() => config.tierLabel(props.tier))
// Theme colours (plugins/branding.ts) with AA contrast as outlined text in both light and dark (FE-032).
const color = computed(() => TIER_COLOR[Math.min(Math.max(props.tier, 1), 3) - 1])
</script>

<template>
  <v-chip :color="color" :size="size" variant="tonal" label data-testid="tier-chip">{{ label }}</v-chip>
</template>
