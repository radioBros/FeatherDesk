<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import AppSnackbar from '@/components/AppSnackbar.vue'
</script>

<template>
  <router-view />
  <ConfirmDialog />
  <AppSnackbar />
</template>

<style>
html,
body {
  font-family: var(--st-font, system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif);
}
.v-application {
  font-family: inherit;
}
/*
 * Tonal controls in a brand colour: the label is the brand colour on its own 12 % tint, which falls
 * under 4.5:1 for light brand colours. Mixing 25 % toward the text colour keeps the hue and passes AA
 * in both themes (the section hues in theme/palette.ts are already AA and are left alone).
 */
.v-btn--variant-tonal.text-primary,
.v-chip--variant-tonal.text-primary {
  color: color-mix(in srgb, rgb(var(--v-theme-primary)) 75%, rgb(var(--v-theme-on-surface))) !important;
}
.v-btn--variant-tonal.text-secondary,
.v-chip--variant-tonal.text-secondary {
  color: color-mix(in srgb, rgb(var(--v-theme-secondary)) 75%, rgb(var(--v-theme-on-surface))) !important;
}
/*
 * Sign-in pages without a branded background: a soft wash of the brand colour and the deep-purple
 * section hue, and a brand-coloured top edge on the card. Text sits on the solid card only.
 */
.st-login-bg {
  background-image:
    radial-gradient(900px 480px at 8% -8%, rgba(var(--v-theme-primary), 0.16), transparent 70%),
    radial-gradient(800px 460px at 100% 0%, rgba(var(--v-theme-nav-branding), 0.12), transparent 70%),
    radial-gradient(700px 420px at 50% 115%, rgba(var(--v-theme-nav-kb), 0.1), transparent 70%);
}
.st-login-card {
  border-top: 4px solid rgb(var(--v-theme-primary)) !important;
  border-image: linear-gradient(90deg, rgb(var(--v-theme-primary)), rgb(var(--v-theme-nav-branding))) 1 0 0 0;
}
/* The current page in a breadcrumb trail is not a disabled control: keep it readable (FE-032, WCAG 1.4.3). */
.v-breadcrumbs-item--disabled {
  opacity: var(--v-medium-emphasis-opacity);
}
</style>
