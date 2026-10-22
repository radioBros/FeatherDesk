<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts" generic="T extends object">
/**
 * The table of every list: `v-data-table-virtual` (only the rows in view are
 * in the DOM) over the columns of useTableColumns — the user's visibility,
 * order and widths, with a ColumnResizer on every header edge.
 *
 * Sorting, by header click:
 * - `server-sort` (cursor lists, not every row is loaded): a click emits
 *   `update:sort` with the column's API `sort` value (ColumnDef.sort) or null
 *   (the endpoint's default order); the page refetches from the first cursor.
 *   Only columns with `sort` are sortable, and the rows keep the order the
 *   server sent: a no-op `sortRaw` stops Vuetify re-sorting them by raw value.
 * - otherwise (every row loaded): Vuetify sorts in the browser, by
 *   ColumnDef.sortValue or the item field named like the column.
 *
 * Cursor lists pass `has-more` and handle `load-more`: scrolling near the end
 * of the table, or a page that does not fill it, asks for the next page. The
 * body is focusable, so the keyboard (arrows, Page Down, End) scrolls and
 * loads the same way; there is no button.
 *
 * Declared columns are filled through `item.<key>` slots; `selectable` adds the
 * pinned selection column (v-model = selected ids), `actions` a trailing
 * `item.actions` column. Other attributes (density, row-props, @click:row, …)
 * go to v-data-table-virtual.
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useAttrs, useSlots, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { DEFAULT_COLUMN_WIDTH } from '@/composables/columnLayout'
import type { TableColumns } from '@/composables/useTableColumns'
import ColumnResizer from './ColumnResizer.vue'

defineOptions({ inheritAttrs: false })

const props = withDefaults(
  defineProps<{
    cols: TableColumns
    items: T[]
    loading?: boolean
    serverSort?: boolean
    /** The API `sort` value in effect (server-sort); null = the endpoint's default. */
    sort?: string | null
    hasMore?: boolean
    selectable?: boolean
    actions?: boolean
    /** CSS max-height of the scrolling body; the table is shorter when its rows are. */
    maxHeight?: string
  }>(),
  { sort: null, maxHeight: 'max(320px, calc(100dvh - 280px))' },
)
const emit = defineEmits<{ 'update:sort': [value: string | null]; 'load-more': [] }>()
defineSlots<Record<string, ((p: any) => any) | undefined>>()

const { t } = useI18n()
const attrs = useAttrs()
const slots = useSlots()

const SELECT_WIDTH = 56
const ACTIONS_WIDTH = 120
const SERVER_ORDER = () => 0
const px = (w: number | undefined) => (w === undefined ? {} : { width: w, minWidth: w, maxWidth: w })

// Undeclared columns are measured and stored by position (`:<n>`, columnLayout).
const selectKey = ':0'
const actionsKey = computed(() => `:${props.selectable ? 1 : 0}`)

const declared = computed(() => {
  const sized = props.cols.sized.value
  const byKey = new Map(props.cols.all.value.map((c) => [c.key, c]))
  return props.cols.visibleKeys.value.flatMap((k) => {
    const c = byKey.get(k)
    if (!c) return []
    const sortable = props.serverSort ? !!c.sort : c.sortValue !== false
    return [
      {
        key: c.key,
        title: c.label,
        align: c.align,
        sortable,
        ...(props.serverSort && sortable ? { sortRaw: SERVER_ORDER } : {}),
        ...(!props.serverSort && typeof c.sortValue === 'function' ? { value: c.sortValue as (item: unknown) => unknown } : {}),
        ...px(sized ? (props.cols.width(c.key) ?? DEFAULT_COLUMN_WIDTH) : c.width),
        // The column key on its header and cells (tests, styling).
        headerProps: { 'data-col': c.key },
        cellProps: { 'data-col': c.key },
      },
    ]
  })
})

const headers = computed(() => {
  const sized = props.cols.sized.value
  const extra = (key: string, w: number) => px(sized ? (props.cols.width(key) ?? w) : undefined)
  return [
    ...(props.selectable ? [{ key: 'data-table-select', fixed: true, ...extra(selectKey, SELECT_WIDTH) }] : []),
    ...declared.value,
    ...(props.actions ? [{ key: 'actions', title: t('app.actions'), sortable: false, align: 'end' as const, ...extra(actionsKey.value, ACTIONS_WIDTH) }] : []),
  ]
})

/** Table width in px once the user has resized a column, else 0 (automatic layout). */
const tableWidth = computed(() => (props.cols.sized.value ? headers.value.reduce((s, h) => s + (('width' in h && h.width) || 0), 0) : 0))

// ---- sorting --------------------------------------------------------------

type SortItem = { key: string; order?: boolean | 'asc' | 'desc' }

const serverSortBy = computed<SortItem[]>(() => {
  for (const c of props.cols.all.value) {
    if (!c.sort || !props.cols.show(c.key)) continue
    if (c.sort.asc === props.sort) return [{ key: c.key, order: 'asc' }]
    if (c.sort.desc === props.sort) return [{ key: c.key, order: 'desc' }]
  }
  return []
})

function onSortBy(v: SortItem[]) {
  if (!props.serverSort) return
  const s = v[0]
  const c = s ? props.cols.all.value.find((x) => x.key === s.key) : undefined
  const next = c?.sort ? (s!.order === 'desc' ? c.sort.desc : c.sort.asc) : null
  if (next !== props.sort) emit('update:sort', next)
}

// ---- slots ------------------------------------------------------------------

// header.* of the declared columns are rendered by the header loop (title, sort
// icon, resizer); every other slot goes straight through. A function, not a
// computed: the slots object is not reactive, and a parent may add slots later
// (e.g. custom-field columns once their definitions load).
const passSlots = () => Object.keys(slots).filter((n) => !(n.startsWith('header.') && props.cols.all.value.some((c) => `header.${c.key}` === n)))

// ---- load more on scroll -------------------------------------------------------

const NEAR_END = 240
const table = ref<{ $el: HTMLElement; scrollToIndex: (index: number) => void }>()
let wrapper: HTMLElement | null = null

function checkEnd() {
  // A hidden table (inactive tab) has no height: never page through it.
  if (!props.hasMore || props.loading || !wrapper?.clientHeight) return
  if (wrapper.scrollTop + wrapper.clientHeight >= wrapper.scrollHeight - NEAR_END) emit('load-more')
}
// The inner table is remounted when it got stuck (below): wire its body again.
function attach() {
  wrapper?.removeEventListener('scroll', checkEnd)
  wrapper = table.value?.$el.querySelector<HTMLElement>('.v-table__wrapper') ?? null
  // The body scrolls (both ways): reachable from the keyboard even when no cell holds a control (FE-032).
  if (wrapper) wrapper.tabIndex = 0
  wrapper?.addEventListener('scroll', checkEnd, { passive: true })
  void nextTick(checkEnd)
}
onMounted(attach)
onBeforeUnmount(() => wrapper?.removeEventListener('scroll', checkEnd))
watch(
  () => [props.items.length, props.loading, props.hasMore],
  () => void nextTick(checkEnd),
)

// The body sizes to its rows (up to max-height), so Vuetify's visible range
// follows a height that settles later and is only recalculated on scroll and
// resize. A short list that grows (a row added) can stay a row short, with no
// scroll ever coming to fix it. After the row count changes, a body that does
// not scroll but draws fewer rows than it has is remounted: nothing to lose,
// as there is no scroll position.
const renderKey = ref(0)
watch(
  () => props.items.length,
  () =>
    void nextTick(() =>
      requestAnimationFrame(() => {
        if (!wrapper?.clientHeight || wrapper.scrollHeight > wrapper.clientHeight + 1) return
        if (wrapper.querySelectorAll('tbody > tr.v-data-table__tr').length < props.items.length) renderKey.value++
      }),
    ),
)
watch(renderKey, () => void nextTick(attach))

defineExpose({
  /** Scrolls a row into view (rows outside it are not in the DOM). */
  scrollToIndex: (index: number) => table.value?.scrollToIndex(index),
})
</script>

<template>
  <div
    class="fd-dt"
    :class="{ 'fd-dt--sized': tableWidth > 0, 'fd-dt--clickable': !!attrs['onClick:row'] }"
    :style="{ '--fd-dt-max-height': maxHeight, ...(tableWidth > 0 ? { '--fd-dt-width': `${tableWidth}px` } : {}) }"
  >
    <v-data-table-virtual
      ref="table"
      :key="renderKey"
      item-value="id"
      v-bind="{ ...attrs, ...(serverSort ? { sortBy: serverSortBy } : {}) }"
      :headers="headers"
      :items="items"
      :loading="loading"
      :show-select="selectable"
      fixed-header
      @update:sort-by="onSortBy"
    >
      <template v-for="h in declared" :key="h.key" #[`header.${h.key}`]="slotData">
        <slot v-if="slots[`header.${h.key}`]" :name="`header.${h.key}`" v-bind="slotData" />
        <div v-else class="v-data-table-header__content">
          <span>{{ slotData.column.title }}</span>
          <v-icon v-if="slotData.column.sortable" class="v-data-table-header__sort-icon" :icon="slotData.getSortIcon(slotData.column)" />
        </div>
        <ColumnResizer :cols="cols" :col="h.key" :label="h.title" />
      </template>
      <template v-for="name in passSlots()" :key="name" #[name]="slotData">
        <slot :name="name" v-bind="slotData ?? {}" />
      </template>
    </v-data-table-virtual>
  </div>
</template>

<style scoped>
.fd-dt :deep(.v-table__wrapper) {
  max-height: var(--fd-dt-max-height);
}
.fd-dt :deep(.v-table__wrapper:focus-visible) {
  outline: 2px solid rgb(var(--v-theme-primary));
  outline-offset: -2px;
}
.fd-dt--clickable :deep(tbody tr) {
  cursor: pointer;
}
/* Resized: every column has its own px width; cells stay one line and clip,
   so row heights hold under the virtual scroller. */
.fd-dt--sized :deep(.v-table__wrapper > table) {
  table-layout: fixed;
  width: var(--fd-dt-width);
}
.fd-dt--sized :deep(.v-table__wrapper > table > thead > tr > th),
.fd-dt--sized :deep(.v-table__wrapper > table > tbody > tr > td) {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
</style>
