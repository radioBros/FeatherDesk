<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * The confirm dialog and the snackbar are lazy chunks (FE-031): each loads when
 * it is first needed (or once the browser is idle) and then stays mounted, so
 * VDialog and VSnackbar are not part of any area's initial JS.
 */
import { defineAsyncComponent, onMounted, ref, watchEffect } from 'vue'
import { confirmState } from '@/composables/useConfirm'
import { useUiStore } from '@/stores/ui'

const loadConfirm = () => import('@/components/ConfirmDialog.vue')
const loadSnackbar = () => import('@/components/AppSnackbar.vue')
const ConfirmDialog = defineAsyncComponent(loadConfirm)
const AppSnackbar = defineAsyncComponent(loadSnackbar)

const confirm = confirmState()
const ui = useUiStore()
const needConfirm = ref(false)
const needSnackbar = ref(false)
watchEffect(() => {
  if (confirm.open) needConfirm.value = true
  if (ui.toasts.length) needSnackbar.value = true
})

// Warm both chunks after the first paint so the first confirm or toast shows at once.
onMounted(() => {
  const idle = (cb: () => void) => ('requestIdleCallback' in window ? window.requestIdleCallback(cb, { timeout: 3000 }) : setTimeout(cb, 1500))
  idle(() => void Promise.all([loadConfirm(), loadSnackbar()]).catch(() => undefined))
})
</script>

<template>
  <router-view />
  <ConfirmDialog v-if="needConfirm" />
  <AppSnackbar v-if="needSnackbar" />
</template>

<style>
html,
body {
  font-family: var(--fd-font, system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif);
}
.v-application {
  font-family: inherit;
}
/*
 * Input density (FE-032): fields are compact and equal in height. A checkbox or switch that shares a
 * row with inputs gets the same 40px control row and centres on it; hit targets stay >= 24px (WCAG 2.2).
 */
.v-input--density-compact.v-checkbox > .v-input__control,
.v-input--density-compact.v-switch > .v-input__control {
  min-height: 40px;
  align-items: center;
}
/*
 * A one-row textarea is as tall as a text field and grows with its content. Taller ones keep the height of
 * their `rows` (Vuetify's auto-grow minimum): a plain 40px here beat it and left every editor two lines high.
 */
.v-textarea.v-input--density-compact .v-field__input {
  min-height: var(--v-textarea-min-height, 40px);
}
.v-textarea.v-input--density-compact textarea {
  line-height: 1.5;
}
/* A small chip (26px) inside a compact field made it 42px; 24px keeps it on the 40px row. */
.v-input--density-compact .v-field__input .v-chip.v-chip--size-small {
  --v-chip-height: 24px;
}
/* Chrome pads the date/time editor by 1px top and bottom, which made date fields 2px taller. */
.v-field__input::-webkit-datetime-edit,
.v-field__input::-webkit-datetime-edit-fields-wrapper {
  padding-top: 0;
  padding-bottom: 0;
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
.fd-login-bg {
  background-image:
    radial-gradient(900px 480px at 8% -8%, rgba(var(--v-theme-primary), 0.16), transparent 70%),
    radial-gradient(800px 460px at 100% 0%, rgba(var(--v-theme-nav-branding), 0.12), transparent 70%),
    radial-gradient(700px 420px at 50% 115%, rgba(var(--v-theme-nav-kb), 0.1), transparent 70%);
}
.fd-login-card {
  border-top: 4px solid rgb(var(--v-theme-primary)) !important;
  border-image: linear-gradient(90deg, rgb(var(--v-theme-primary)), rgb(var(--v-theme-nav-branding))) 1 0 0 0;
}
/* The current page in a breadcrumb trail is not a disabled control: keep it readable (FE-032, WCAG 1.4.3). */
.v-breadcrumbs-item--disabled {
  opacity: var(--v-medium-emphasis-opacity);
}

html .v-dialog>.v-overlay__content>.v-card>.v-card-item+.v-card-text, 
html .v-dialog>.v-overlay__content>form>.v-card>.v-card-item+.v-card-text {
 padding-top: 12px;
}
</style>
