<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Table toolbar button: choose which columns a table shows and in which order
 * (see useTableColumns). Drag a column by its handle (mouse or touch), or focus
 * the handle and press ↑/↓.
 */
import { nextTick, ref } from 'vue'
import { mdiDragVertical, mdiTableColumn } from '@mdi/js'
import type { TableColumns } from '@/composables/useTableColumns'

const props = defineProps<{ cols: TableColumns; bar?: boolean }>()
const dragging = ref<string | null>(null)
const list = ref<HTMLElement>()

function indexOf(key: string) {
  return props.cols.all.value.findIndex((c) => c.key === key)
}

function onPointerdown(e: PointerEvent, key: string) {
  if (e.button !== 0) return
  e.preventDefault()
  dragging.value = key
  // Window listeners, not pointer capture: the item moves in the DOM while
  // dragged, which ends a capture.
  const move = (ev: PointerEvent) => {
    const over = document.elementFromPoint(ev.clientX, ev.clientY)?.closest<HTMLElement>('[data-col-item]')
    const target = over?.dataset.colItem
    if (target && target !== key) props.cols.move(key, indexOf(target))
  }
  const end = () => {
    dragging.value = null
    window.removeEventListener('pointermove', move)
    window.removeEventListener('pointerup', end)
    window.removeEventListener('pointercancel', end)
  }
  window.addEventListener('pointermove', move)
  window.addEventListener('pointerup', end)
  window.addEventListener('pointercancel', end)
}

async function onKeydown(e: KeyboardEvent, key: string) {
  const d = e.key === 'ArrowUp' ? -1 : e.key === 'ArrowDown' ? 1 : 0
  if (!d) return
  e.preventDefault()
  props.cols.move(key, indexOf(key) + d)
  await nextTick()
  list.value?.querySelector<HTMLElement>(`[data-col-item="${CSS.escape(key)}"] .fd-col-drag`)?.focus()
}
</script>

<template>
  <div :class="bar === false ? '' : 'd-flex justify-end px-2 pt-1'" data-testid="columns-bar">
    <v-menu :close-on-content-click="false" location="bottom end">
      <template #activator="{ props: p }">
        <v-btn v-bind="p" variant="text" size="small" :prepend-icon="mdiTableColumn" :aria-label="$t('app.columns.button')" data-testid="columns-menu">
          {{ $t('app.columns.button') }}
        </v-btn>
      </template>
      <v-card min-width="260" max-width="340">
        <v-card-title class="text-subtitle-2 pb-0">{{ $t('app.columns.title') }}</v-card-title>
        <v-card-subtitle class="text-caption pb-1">{{ $t('app.columns.hint') }}</v-card-subtitle>
        <div ref="list" role="list" class="py-1" data-testid="columns-list">
          <div
            v-for="c in cols.all.value"
            :key="c.key"
            role="listitem"
            class="fd-col-item d-flex align-center pr-2"
            :class="{ 'fd-col-item--dragging': dragging === c.key }"
            :data-col-item="c.key"
          >
            <button
              type="button"
              class="fd-col-drag"
              :aria-label="$t('app.columns.move', { column: c.label })"
              :data-testid="`column-drag-${c.key}`"
              @pointerdown="onPointerdown($event, c.key)"
              @keydown="onKeydown($event, c.key)"
            >
              <v-icon :icon="mdiDragVertical" size="20" />
            </button>
            <v-checkbox
              :model-value="cols.show(c.key)"
              :label="c.label"
              :disabled="c.locked"
              :title="c.locked ? $t('app.columns.locked') : undefined"
              :data-testid="`column-toggle-${c.key}`"
              hide-details
              @update:model-value="(on: boolean | null) => cols.set(c.key, !!on)"
            />
          </div>
        </div>
        <v-card-actions>
          <v-btn variant="text" size="small" :disabled="cols.isDefault.value" data-testid="columns-reset" @click="cols.reset()">{{ $t('app.columns.reset') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-menu>
  </div>
</template>

<style scoped>
.fd-col-item {
  min-height: 40px;
  border-radius: 8px;
  user-select: none;
}
.fd-col-item--dragging {
  background: rgba(var(--v-theme-primary), 0.12);
}
.fd-col-drag {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 40px;
  flex: 0 0 auto;
  padding: 0;
  border: 0;
  background: none;
  cursor: grab;
  touch-action: none;
  color: rgba(var(--v-theme-on-surface), var(--v-medium-emphasis-opacity));
  border-radius: 6px;
}
.fd-col-item--dragging .fd-col-drag {
  cursor: grabbing;
}
.fd-col-drag:focus-visible {
  outline: 2px solid rgb(var(--v-theme-primary));
  outline-offset: -2px;
}
</style>
