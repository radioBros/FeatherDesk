<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
/**
 * The top bar of both areas (agent panel and portal). The spacing rules live
 * here only: 8 px between the actions, extra padding at both edges and a
 * 40 px minimum hit target for every button. Layouts fill the default slot
 * (title, search), `prepend` (menu button) and `append` (the actions).
 */
defineOptions({ inheritAttrs: false })

// Sticky panels (TwoColumnLayout) sit under the bar: publish its real bottom edge plus a gap as
// --st-sticky-top on <html>. The bar differs between areas and changes height on mobile.
const bar = ref<{ $el: HTMLElement } | null>(null)
let ro: ResizeObserver | undefined
function publish() {
  const el = bar.value?.$el
  if (!el) return
  const h = Math.round(el.getBoundingClientRect().bottom)
  document.documentElement.style.setProperty('--st-sticky-top', `${h + 16}px`)
}
onMounted(() => {
  publish()
  if (typeof ResizeObserver !== 'undefined' && bar.value?.$el) {
    ro = new ResizeObserver(publish)
    ro.observe(bar.value.$el)
  }
  window.addEventListener('resize', publish)
})
onBeforeUnmount(() => {
  ro?.disconnect()
  window.removeEventListener('resize', publish)
})
</script>

<template>
  <v-app-bar ref="bar" v-bind="$attrs" flat border density="comfortable" class="st-appbar">
    <template v-if="$slots.prepend" #prepend><slot name="prepend" /></template>
    <slot />
    <template #append>
      <div class="st-appbar-actions" data-testid="appbar-actions"><slot name="append" /></div>
    </template>
  </v-app-bar>
</template>

<style>
.st-appbar .v-toolbar__content { padding-inline: 12px 16px; }
.st-appbar .v-toolbar__append { margin-inline: auto 0; padding-inline-start: 8px; }
.st-appbar .v-toolbar__prepend { margin-inline: 0 8px; }
.st-appbar-actions { display: flex; align-items: center; gap: 8px; min-width: 0; }
.st-appbar .v-toolbar__content .v-btn { min-height: 40px; min-width: 40px; }
.st-appbar .v-toolbar__content .v-btn:not(.v-btn--icon) { height: 40px; }
.st-appbar .v-toolbar__content .v-btn.v-btn--icon { width: 40px; height: 40px; }
</style>
