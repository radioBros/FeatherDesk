<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** A single horizontal proportion bar (e.g. SLA met vs breached) in plain SVG (FE-043). */
import { computed } from 'vue'

const props = defineProps<{ parts: { label: string; value: number; color: string }[]; label: string }>()
const total = computed(() => props.parts.reduce((n, p) => n + Math.max(0, p.value), 0))
const segments = computed(() => {
  let x = 0
  return props.parts.map((p) => {
    const w = total.value ? (Math.max(0, p.value) / total.value) * 100 : 0
    const seg = { ...p, x, w, pct: total.value ? Math.round((p.value / total.value) * 100) : 0 }
    x += w
    return seg
  })
})
</script>

<template>
  <figure class="ma-0" data-testid="ratio-bar">
    <svg viewBox="0 0 100 8" preserveAspectRatio="none" width="100%" height="16" role="img" :aria-label="label">
      <title>{{ label }}</title>
      <rect x="0" y="0" width="100" height="8" class="st-track" />
      <rect v-for="s in segments" :key="s.label" :x="s.x" y="0" :width="s.w" height="8" :style="{ fill: `rgb(var(--v-theme-${s.color}))` }" data-testid="ratio-segment">
        <title>{{ s.label }}: {{ s.value }} ({{ s.pct }}%)</title>
      </rect>
    </svg>
    <figcaption class="d-flex flex-wrap ga-3 text-caption mt-1">
      <span v-for="s in segments" :key="s.label" class="d-inline-flex align-center ga-1">
        <span class="st-swatch" :style="{ background: `rgb(var(--v-theme-${s.color}))` }" />{{ s.label }}: {{ s.value }} ({{ s.pct }}%)
      </span>
    </figcaption>
  </figure>
</template>

<style scoped>
.st-track { fill: rgba(var(--v-theme-on-surface), 0.08); }
.st-swatch { width: 10px; height: 10px; border-radius: 2px; display: inline-block; }
</style>
