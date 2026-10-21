<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed } from 'vue'
import { useConfigStore } from '@/stores/config'
import BrandLogo from '@/components/BrandLogo.vue'

/**
 * Branded frame for the anonymous portal pages (FE-050): logo, title and
 * subtitle, background colour or image from `/auth/config` branding.
 */
const props = defineProps<{ title?: string; subtitle?: string; branded?: boolean }>()
const config = useConfigStore()
const b = computed(() => config.branding)
const bg = computed(() => {
  const style: Record<string, string> = {}
  if (b.value.login_background_color) style.backgroundColor = b.value.login_background_color
  if (b.value.login_background_url) {
    style.backgroundImage = `url("${encodeURI(b.value.login_background_url)}")`
    style.backgroundSize = 'cover'
    style.backgroundPosition = 'center'
  }
  return style
})
const heading = computed(() => props.title ?? (props.branded ? b.value.login_title : undefined))
const sub = computed(() => props.subtitle ?? (props.branded ? b.value.login_subtitle : undefined))
</script>

<template>
  <div class="st-auth-bg fill-height" :class="{ 'st-login-bg': !Object.keys(bg).length }" :style="bg" data-testid="auth-background">
    <v-container class="py-10" style="max-width: 460px">
      <v-card border flat class="pa-2 st-login-card">
        <div class="text-center pt-4"><BrandLogo :height="40" /></div>
        <v-card-title v-if="heading" class="text-h5 text-wrap text-center" data-testid="auth-title"><h1 class="text-h5">{{ heading }}</h1></v-card-title>
        <v-card-subtitle v-if="sub" class="text-wrap text-center" data-testid="auth-subtitle">{{ sub }}</v-card-subtitle>
        <v-card-text>
          <slot />
        </v-card-text>
      </v-card>
      <slot name="below" />
    </v-container>
  </div>
</template>

<style scoped>
.st-auth-bg { min-height: 100%; }
</style>
