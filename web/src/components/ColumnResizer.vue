<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Drag handle on the right edge of a header cell: resizes the column (pointer
 * or ←/→ on the focused handle). Widths are stored by useTableColumns; the
 * first resize freezes every column at its current width (columnLayout).
 */
import { onMounted, ref } from 'vue'
import { MIN_COLUMN_WIDTH, type TableColumns } from '@/composables/useTableColumns'
import { measureHeaderRow } from '@/composables/columnLayout'

const props = defineProps<{ cols: TableColumns; col: string; label?: string }>()
const STEP = 16
const el = ref<HTMLElement>()
const measured = ref(0)

onMounted(() => {
  measured.value = Math.round(el.value?.closest('th')?.getBoundingClientRect().width ?? 0)
})

/** Current width of this column, freezing every column first on the first resize. */
function begin(): number {
  const th = el.value?.closest('th')
  if (!th) return MIN_COLUMN_WIDTH
  if (!props.cols.sized.value && th.parentElement) props.cols.setWidths(measureHeaderRow(th.parentElement), false)
  return props.cols.width(props.col) ?? th.getBoundingClientRect().width
}

// After a drag the browser sends `click` to the common ancestor of the press
// and release targets: the <th> itself when the drag ends inside the column,
// which would toggle its sort. Drop that one click; if none comes, stop
// waiting on the next tick.
function swallowNextClick() {
  const swallow = (e: MouseEvent) => {
    e.stopPropagation()
    e.preventDefault()
  }
  window.addEventListener('click', swallow, { capture: true, once: true })
  setTimeout(() => window.removeEventListener('click', swallow, { capture: true }), 0)
}

function onPointerdown(e: PointerEvent) {
  if (e.button !== 0 || !el.value) return
  e.preventDefault()
  const startX = e.clientX
  const startW = begin()
  let moved = false
  const move = (ev: PointerEvent) => {
    moved = true
    props.cols.setWidths({ [props.col]: startW + ev.clientX - startX }, false)
  }
  const end = () => {
    window.removeEventListener('pointermove', move)
    window.removeEventListener('pointerup', end)
    window.removeEventListener('pointercancel', end)
    if (moved) swallowNextClick()
    props.cols.saveWidths()
  }
  window.addEventListener('pointermove', move)
  window.addEventListener('pointerup', end)
  window.addEventListener('pointercancel', end)
}

function onKeydown(e: KeyboardEvent) {
  const d = e.key === 'ArrowRight' ? STEP : e.key === 'ArrowLeft' ? -STEP : 0
  if (!d) return
  e.preventDefault()
  props.cols.setWidths({ [props.col]: begin() + d })
}
</script>

<template>
  <span
    ref="el"
    class="fd-col-resize"
    role="separator"
    aria-orientation="vertical"
    tabindex="0"
    :aria-label="$t('app.columns.resize', { column: label ?? col })"
    :aria-valuenow="cols.width(col) ?? measured"
    :aria-valuemin="MIN_COLUMN_WIDTH"
    :data-resize-col="col"
    :data-testid="`column-resize-${col}`"
    @pointerdown.stop="onPointerdown"
    @mousedown.stop
    @click.stop
    @keydown.stop="onKeydown"
  />
</template>

<style>
/* The handle sits in its header cell; a sticky header cell already holds it. */
th:not(.v-data-table__th--sticky):has(> .fd-col-resize),
th:not(.v-data-table__th--sticky):has(> * > .fd-col-resize) {
  position: relative;
}
.fd-col-resize {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  width: 8px;
  cursor: col-resize;
  touch-action: none;
  z-index: 1;
}
.fd-col-resize::after {
  content: '';
  position: absolute;
  top: 25%;
  bottom: 25%;
  left: 3px;
  width: 2px;
  border-radius: 1px;
  background: rgba(var(--v-border-color), var(--v-border-opacity));
  opacity: 0;
  transition: opacity 0.15s;
}
th:hover > .fd-col-resize::after,
th:hover > * > .fd-col-resize::after,
.fd-col-resize:active::after,
.fd-col-resize:focus-visible::after {
  opacity: 1;
  background: rgb(var(--v-theme-primary));
}
.fd-col-resize:focus-visible {
  outline: none;
}
</style>
