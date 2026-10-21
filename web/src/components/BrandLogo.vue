<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed } from 'vue'
import { useTheme } from 'vuetify'
import { useConfigStore } from '@/stores/config'
import { logoFor } from '@/plugins/branding'

withDefaults(defineProps<{ height?: number }>(), { height: 32 })
const config = useConfigStore()
const theme = useTheme()
const src = computed(() => logoFor(config.branding, theme.current.value.dark))
</script>

<template>
  <span class="d-inline-flex align-center ga-2" data-testid="brand">
    <img v-if="src" :src="src" :alt="config.branding.product_name" :style="{ height: height + 'px' }" />
    <span v-else class="text-h6 font-weight-bold">{{ config.branding.product_name }}</span>
  </span>
</template>
