<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed } from 'vue'
import { useTheme } from 'vuetify'
import { useConfigStore } from '@/stores/config'
import { isDefaultLogo, logoFor } from '@/plugins/branding'

withDefaults(defineProps<{ height?: number }>(), { height: 32 })
const config = useConfigStore()
const theme = useTheme()
const src = computed(() => config.project?.logo_url || logoFor(config.branding, theme.current.value.dark))
// The default FeatherDesk mark has no wordmark, so the product name is shown beside it.
const showName = computed(() => !config.project?.logo_url && isDefaultLogo(config.branding))
</script>

<template>
  <span class="d-inline-flex align-center ga-2" data-testid="brand">
    <img :src="src" :alt="config.project?.portal_title || config.branding.product_name" :style="{ height: height + 'px' }" />
    <span v-if="showName" class="text-h6 font-weight-bold">{{ config.project?.portal_title || config.branding.product_name }}</span>
  </span>
</template>
