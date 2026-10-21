<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Two-section page (e.g. ticket thread + properties). The page scrolls normally
 * with the LONGER column; whichever column is SHORTER becomes sticky beside it,
 * so it stays in view. Nothing gets an internal scrollbar.
 *
 * If the shorter column is itself taller than the viewport, its sticky `top`
 * becomes negative (viewport − height − gap): it scrolls with the page until
 * its bottom is visible, then sticks there, so no part of it is ever unreachable.
 *
 * From the `breakpoint` up only; below it the columns stack and nothing sticks.
 * The header offset is `--st-sticky-top`, kept current by `AppBar.vue`.
 * `aside-start` puts the aside first; `main-fr` / `aside-fr` set the ratio.
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

const props = withDefaults(defineProps<{ asideStart?: boolean; mainFr?: number; asideFr?: number; breakpoint?: 'md' | 'lg' }>(), {
  asideStart: false,
  mainFr: 8,
  asideFr: 4,
  breakpoint: 'md',
})

const mainEl = ref<HTMLElement | null>(null)
const asideEl = ref<HTMLElement | null>(null)
const stickySide = ref<'main' | 'aside' | null>(null)
const stickyTop = ref<string>('var(--st-sticky-top, 72px)')

const BOTTOM_GAP = 16
const minWidth = computed(() => (props.breakpoint === 'lg' ? 1280 : 960))

function headerOffset(): number {
  const v = getComputedStyle(document.documentElement).getPropertyValue('--st-sticky-top')
  const n = parseFloat(v)
  return Number.isFinite(n) ? n : 72
}

function measure(): void {
  const main = mainEl.value
  const aside = asideEl.value
  if (!main || !aside || window.innerWidth < minWidth.value) {
    stickySide.value = null
    return
  }
  // Natural heights of the content (the column wrappers stretch in the grid).
  const mainH = (main.firstElementChild as HTMLElement | null)?.offsetHeight ?? main.offsetHeight
  const asideH = (aside.firstElementChild as HTMLElement | null)?.offsetHeight ?? aside.offsetHeight
  if (Math.abs(mainH - asideH) < 8) {
    stickySide.value = null
    return
  }
  const shorter = mainH < asideH ? 'main' : 'aside'
  const shortH = Math.min(mainH, asideH)
  stickySide.value = shorter
  const offset = headerOffset()
  const room = window.innerHeight - offset - BOTTOM_GAP
  // Fits: stick under the header. Taller than the viewport: stick by its bottom edge.
  stickyTop.value = shortH <= room ? `${offset}px` : `${window.innerHeight - shortH - BOTTOM_GAP}px`
}

let ro: ResizeObserver | null = null
let raf = 0
const schedule = (): void => {
  cancelAnimationFrame(raf)
  raf = requestAnimationFrame(measure)
}

onMounted(() => {
  ro = new ResizeObserver(schedule)
  for (const el of [mainEl.value, asideEl.value]) {
    const inner = el?.firstElementChild
    if (inner) ro.observe(inner)
  }
  window.addEventListener('resize', schedule)
  schedule()
})
onBeforeUnmount(() => {
  ro?.disconnect()
  window.removeEventListener('resize', schedule)
  cancelAnimationFrame(raf)
})

const stickyStyle = computed(() => ({ position: 'sticky' as const, top: stickyTop.value }))
</script>

<template>
  <div
    class="st-two"
    :class="[`st-two--${breakpoint}`, { 'st-two--aside-start': asideStart }]"
    :style="{ '--st-main-fr': `${mainFr}fr`, '--st-aside-fr': `${asideFr}fr` }"
    :data-sticky="stickySide ?? 'none'"
    data-testid="two-column"
  >
    <div v-if="asideStart" ref="asideEl" class="st-two__aside" data-testid="two-column-aside">
      <div class="st-two__inner" :style="stickySide === 'aside' ? stickyStyle : undefined"><slot name="aside" /></div>
    </div>
    <div ref="mainEl" class="st-two__main" data-testid="two-column-main">
      <div class="st-two__inner" :style="stickySide === 'main' ? stickyStyle : undefined"><slot name="main" /></div>
    </div>
    <div v-if="!asideStart" ref="asideEl" class="st-two__aside" data-testid="two-column-aside">
      <div class="st-two__inner" :style="stickySide === 'aside' ? stickyStyle : undefined"><slot name="aside" /></div>
    </div>
  </div>
</template>

<style>
.st-two { display: grid; grid-template-columns: minmax(0, 1fr); gap: 24px; }
.st-two__main, .st-two__aside { min-width: 0; }
.st-two__inner { display: flex; flex-direction: column; gap: 12px; }
@media (min-width: 960px) {
  .st-two--md { grid-template-columns: minmax(0, var(--st-main-fr)) minmax(0, var(--st-aside-fr)); }
  .st-two--md.st-two--aside-start { grid-template-columns: minmax(0, var(--st-aside-fr)) minmax(0, var(--st-main-fr)); }
}
@media (min-width: 1280px) {
  .st-two--lg { grid-template-columns: minmax(0, var(--st-main-fr)) minmax(0, var(--st-aside-fr)); }
  .st-two--lg.st-two--aside-start { grid-template-columns: minmax(0, var(--st-aside-fr)) minmax(0, var(--st-main-fr)); }
}
</style>
