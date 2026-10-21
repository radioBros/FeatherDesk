<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Grouped vertical bars in plain SVG (FE-043: no chart library). Colours come
 * from the Vuetify theme variables so the chart follows branding and dark mode.
 */
import { computed } from 'vue'

export interface Series {
  label: string
  /** A theme colour name, e.g. 'primary' → rgb(var(--v-theme-primary)). */
  color: string
}
const props = withDefaults(
  defineProps<{ groups: { label: string; values: number[] }[]; series: Series[]; label: string; height?: number }>(),
  { height: 180 },
)

const BAR = 18
const GAP = 4
const GROUP_GAP = 28
const PAD = { top: 16, bottom: 28, left: 32, right: 8 }

const max = computed(() => Math.max(1, ...props.groups.flatMap((g) => g.values)))
const niceMax = computed(() => {
  const m = max.value
  const pow = 10 ** Math.floor(Math.log10(m))
  return Math.ceil(m / pow) * pow
})
const groupWidth = computed(() => props.series.length * BAR + (props.series.length - 1) * GAP)
const width = computed(() => PAD.left + PAD.right + props.groups.length * groupWidth.value + Math.max(0, props.groups.length - 1) * GROUP_GAP + 16)
const plotH = computed(() => props.height - PAD.top - PAD.bottom)
const y = (v: number) => PAD.top + plotH.value - (v / niceMax.value) * plotH.value
const gx = (gi: number) => PAD.left + 8 + gi * (groupWidth.value + GROUP_GAP)
const bars = computed(() =>
  props.groups.flatMap((g, gi) =>
    g.values.map((v, si) => ({
      key: `${gi}-${si}`,
      x: gx(gi) + si * (BAR + GAP),
      y: y(v),
      h: Math.max(v > 0 ? 1 : 0, PAD.top + plotH.value - y(v)),
      fill: `rgb(var(--v-theme-${props.series[si]?.color ?? 'primary'}))`,
      title: `${g.label} · ${props.series[si]?.label ?? ''}: ${v}`,
    })),
  ),
)
</script>

<template>
  <!-- Scrolls sideways when the bars outgrow the card: focusable so keyboard users can scroll it (FE-032). -->
  <figure class="st-chart" tabindex="0" :aria-label="label" data-testid="grouped-bar-chart">
    <svg :viewBox="`0 0 ${width} ${height}`" :width="width" :height="height" role="img" :aria-label="label">
      <title>{{ label }}</title>
      <line :x1="PAD.left" :x2="width - PAD.right" :y1="y(0)" :y2="y(0)" class="st-axis" />
      <line :x1="PAD.left" :x2="width - PAD.right" :y1="y(niceMax)" :y2="y(niceMax)" class="st-grid" />
      <text :x="PAD.left - 4" :y="y(niceMax) + 4" text-anchor="end" class="st-tick">{{ niceMax }}</text>
      <text :x="PAD.left - 4" :y="y(0) + 4" text-anchor="end" class="st-tick">0</text>
      <rect v-for="b in bars" :key="b.key" :x="b.x" :y="b.y" :width="BAR" :height="b.h" :style="{ fill: b.fill }" rx="2" data-testid="chart-bar">
        <title>{{ b.title }}</title>
      </rect>
      <text v-for="(g, gi) in groups" :key="g.label + gi" :x="gx(gi) + groupWidth / 2" :y="height - 8" text-anchor="middle" class="st-tick">{{ g.label }}</text>
    </svg>
    <figcaption class="d-flex flex-wrap ga-3 text-caption mt-1">
      <span v-for="s in series" :key="s.label" class="d-inline-flex align-center ga-1">
        <span class="st-swatch" :style="{ background: `rgb(var(--v-theme-${s.color}))` }" />{{ s.label }}
      </span>
    </figcaption>
  </figure>
</template>

<style scoped>
.st-chart { margin: 0; max-width: 100%; overflow-x: auto; }
.st-axis { stroke: rgba(var(--v-theme-on-surface), 0.4); }
.st-grid { stroke: rgba(var(--v-theme-on-surface), 0.12); stroke-dasharray: 3 3; }
.st-tick { font-size: 11px; fill: rgba(var(--v-theme-on-surface), 0.72); }
.st-swatch { width: 10px; height: 10px; border-radius: 2px; display: inline-block; }
</style>
