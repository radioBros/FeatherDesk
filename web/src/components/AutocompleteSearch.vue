<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<template>
  <v-select
    v-model="value"
    :items="menuItems"
    :item-title="itemTitle ?? 'title'"
    :item-value="itemValue ?? 'value'"
    :label="label"
    :rules="rules"
    :clearable="clearable"
    :disabled="disabled"
    :loading="loading || undefined"
    ref="selectEl"
    v-bind="$attrs"
    @update:menu="onMenuChange"
  >
    <template
      v-if="!hideSearch"
      #prepend-item
    >
      <div
        class="px-2 pb-0 pt-2 sticky bg-background"
        style="top: -8px; left: 0; right: 0; z-index: 2; position: sticky;"
      >
        <v-text-field
          ref="searchInputEl"
          v-model="searchText"
          :placeholder="placeholder ?? t('common.search')"
          :aria-label="placeholder ?? t('common.search')"
          v-bind="searchAtts ?? {}"
          variant="outlined"
          density="compact"
          autocomplete="off"
          type="search"
          data-lpignore="true"
          data-testid="autocomplete-search-input"
          autofocus
          hide-details
          @keydown="onSearchKeydown"
        />
        <v-divider class="mt-2 mb-0 bg-grey" />
      </div>
    </template>

    <template #no-data>
      <v-list-item
        :title="loading ? t('common.searching') : t('common.noResults')"
        data-testid="autocomplete-search-empty"
      />
    </template>

    <template #item="data">
      <template v-if="!isHidden(data.item)">
        <slot
          name="item"
          v-bind="data"
        >
          <v-list-item
            :title="getItemTitle(data.item)"
            :data-testid="getItemValue(data.item)"
            v-bind="data.props"
          />
        </slot>
      </template>
    </template>

    <template
      v-if="slots.selection || compactActive"
      #selection="data"
    >
      <!-- compact-multiple: chips up to `compactChipLimit`, else ONE summary -->
      <template v-if="compactActive">
        <template v-if="slotKind(data.index) === 'chip'">
          <slot
            name="selection"
            v-bind="data"
          >
            <v-chip
              size="small"
              class="autocomplete-search__chip"
            >
              {{ getItemTitle(data.item) }}
            </v-chip>
          </slot>
        </template>
        <span
          v-else-if="slotKind(data.index) === 'summary'"
          class="autocomplete-search__summary"
          data-test="selection-summary"
        >{{ t('common.nSelected', { n: selectedCount }) }}</span>
      </template>
      <slot
        v-else
        name="selection"
        v-bind="data"
      />
    </template>

    <template
      v-if="slots.chip"
      #chip="data"
    >
      <slot
        name="chip"
        v-bind="data"
      />
    </template>

    <template
      v-if="slots.append"
      #append-item
    >
      <slot name="append" />
    </template>

    <template
      v-if="slots['append-outer']"
      #append
    >
      <slot name="append-outer" />
    </template>
  </v-select>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useAttrs, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  COMPACT_CHIP_LIMIT,
  compactSelectionSlot,
  orderSelectedFirst,
  selectedValues,
  type SelectionValue,
} from '@/utils/compactSelection'

// ---- Types ------------------------------------------------------------------

interface Item {
  title?: string
  value?: unknown
  [key: string]: unknown
}

// ---- Props & emits ----------------------------------------------------------

const props = withDefaults(
  defineProps<{
    modelValue?: unknown
    items?: readonly unknown[]
    label?: string
    placeholder?: string
    rules?: readonly ((v: any) => true | string)[]
    itemTitle?: string | ((item: any) => string)
    itemValue?: string
    clearable?: boolean
    disabled?: boolean
    searchFields?: string[]
    searchAtts?: Record<string, unknown> | null
    /** Filter-bar mode for a `multiple` select (owner, 2026-10-02): up to
     *  `compactChipLimit` values as chips, more as ONE "N items selected", and
     *  the selected options listed first when the menu opens. */
    compactMultiple?: boolean
    compactChipLimit?: number
    /** No search box in the menu (short lists that only need the compact mode). */
    hideSearch?: boolean
    /** Async (remote) mode: the menu's search box calls this (debounced) and the
     *  results replace the items; the client-side filter is skipped. With no
     *  `items` the menu calls `fetchItems('')` on open, otherwise `items` are
     *  the initial options until the user types. */
    fetchItems?: (query: string) => Promise<readonly unknown[]>
    /** Async mode: items that may be selected but are not in the current results
     *  (so the selected title still renders). */
    initialItems?: readonly unknown[]
    debounceMs?: number
  }>(),
  {
    modelValue: undefined,
    items: () => [],
    label: undefined,
    placeholder: undefined,
    rules: () => [],
    itemTitle: undefined,
    itemValue: undefined,
    clearable: false,
    disabled: false,
    searchFields: () => [],
    searchAtts: null,
    compactMultiple: false,
    compactChipLimit: COMPACT_CHIP_LIMIT,
    hideSearch: false,
    fetchItems: undefined,
    initialItems: () => [],
    debounceMs: 250,
  },
)

const emit = defineEmits<{
  'update:modelValue': [value: any]
}>()

const slots = defineSlots<{
  item?(p: { props: Record<string, any>; item: any; index: number; [k: string]: any }): any
  selection?(p: { item: any; index: number; [k: string]: any }): any
  chip?(p: { item: any; props: Record<string, any>; [k: string]: any }): any
  append?(): any
  'append-outer'?(): any
}>()
const attrs = useAttrs()
const { t } = useI18n()

// `multiple` reaches v-select through $attrs: `''` when written bare, a boolean
// when bound. The compact mode only means something on a multi-select.
const compactActive = computed<boolean>(() => {
  const m = attrs.multiple
  return props.compactMultiple && (m === '' || m === true || m === 'true')
})
const selectedCount = computed<number>(() => selectedValues(props.modelValue).length)
const slotKind = (index: number) =>
  compactSelectionSlot(selectedCount.value, index, props.compactChipLimit)

// The selection captured when the menu OPENS: those options are listed first.
// Not live — a click inside the open menu must not move rows under the pointer.
const pinned = ref<SelectionValue[]>([])

// ---- Refs -------------------------------------------------------------------

const selectEl = ref<{ reset: () => void } | null>(null)
const searchInputEl = ref<{ focus: () => void } | null>(null)
const searchText = ref<string>('')

// ---- v-model passthrough ----------------------------------------------------

const value = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
})

// ---- Item helpers -----------------------------------------------------------

const titleKey = computed<string>(() => (typeof props.itemTitle === 'string' ? props.itemTitle : 'title'))
const valueKey = computed<string>(() => props.itemValue ?? 'value')

// In Vuetify 4 the #item slot's `data.item` is the ORIGINAL object passed in
// `:items` (the wrapper is `data.internalItem`). So read the configured
// item-title / item-value key straight off it (supports dotted paths).
const getByPath = (obj: unknown, path: string): unknown =>
  path.split('.').reduce<unknown>((acc, key) => (acc as Record<string, unknown>)?.[key], obj)

// Vuetify may hand a slot its list-item wrapper (`{ raw, title, props }`): read the raw object.
const rawOf = (item: unknown): unknown =>
  item && typeof item === 'object' && 'raw' in item && 'props' in item ? (item as { raw: unknown }).raw : item

const readKey = (item: unknown, key: string): unknown => {
  const o = rawOf(item)
  return key.includes('.') ? getByPath(o, key) : (o as Record<string, unknown>)?.[key]
}

const getItemTitle = (item: unknown): string => {
  if (typeof props.itemTitle === 'function') return String(props.itemTitle(rawOf(item)) ?? '')
  const v = readKey(item, titleKey.value)
  return v == null ? '' : String(v)
}

const rawItemValue = (item: unknown): unknown => readKey(item, valueKey.value)

const getItemValue = (item: unknown): string => {
  const v = rawItemValue(item)
  return v == null ? '' : String(v)
}

// ---- Client-side filter -----------------------------------------------------

const matchesSearch = (item: Item, text: string): boolean => {
  const haystack = [getItemTitle(item), ...props.searchFields.map((f) => item[f])]
    .filter((x) => x != null && x !== '')
    .join(' ')
    .toLowerCase()
  return haystack.includes(text.toLowerCase())
}

// Plain strings / numbers become `{ title, value }`.
const normalizeItems = (raw: readonly unknown[]): Item[] =>
  raw.map((a) => (typeof a === 'string' || typeof a === 'number' ? { title: String(a), value: a } : (a as Item)))

// ---- Async (remote) mode ----------------------------------------------------

const isAsync = computed<boolean>(() => typeof props.fetchItems === 'function')
const remote = ref<Item[] | null>(null) // null: show `items` (the initial options)
const loading = ref<boolean>(false)
const known = new Map<string, Item>() // every item ever seen, to keep selected titles
let seq = 0
let timer: ReturnType<typeof setTimeout> | undefined
let lastQuery: string | null = null

const remember = (list: readonly Item[]): void => {
  for (const it of list) known.set(getItemValue(it), it)
}

const runFetch = async (q: string): Promise<void> => {
  const mine = ++seq
  loading.value = true
  let res: Item[]
  try {
    res = normalizeItems(await props.fetchItems!(q))
  } catch {
    res = []
  }
  if (mine !== seq) return // stale: a newer query started meanwhile
  remember(res)
  remote.value = res
  loading.value = false
}

const onQuery = (q: string, immediate: boolean): void => {
  if (!isAsync.value) return
  clearTimeout(timer)
  lastQuery = q
  const query = q.trim()
  if (query === '' && props.items.length > 0) {
    seq++ // drop in-flight responses
    remote.value = null
    loading.value = false
    return
  }
  seq++ // results of older queries must not land any more
  loading.value = true
  if (immediate) void runFetch(query)
  else timer = setTimeout(() => void runFetch(query), props.debounceMs)
}

watch(searchText, (q) => {
  if (q !== lastQuery) onQuery(q, false)
})
onBeforeUnmount(() => clearTimeout(timer))

// ---- Menu open: clear search + autofocus ------------------------------------

const searchInput = (): HTMLInputElement | null =>
  (searchInputEl.value as unknown as { $el?: HTMLElement } | null)?.$el?.querySelector('input') ?? null

// Vuetify moves focus onto the first list item once the menu has opened, so a
// single focus() at nextTick loses the race: retry until the search input
// actually holds focus (bounded, ~300 ms).
const focusSearch = (): void => {
  let tries = 0
  const attempt = (): void => {
    const el = searchInput()
    if (el && document.activeElement !== el) el.focus()
    if (++tries < 8 && document.activeElement !== el) setTimeout(attempt, 40)
  }
  void nextTick(attempt)
}

// Keys typed in the search box stay there: v-select's list type-ahead must not
// see them. Escape still closes the menu; ArrowDown moves into the list.
const onSearchKeydown = (e: KeyboardEvent): void => {
  if (e.key === 'Escape' || e.key === 'ArrowDown') return
  e.stopPropagation()
}

// While the menu is open, a printable key pressed anywhere in it (e.g. focus
// sitting on a list item) is redirected into the search box.
const onDocumentKeydown = (e: KeyboardEvent): void => {
  if (props.hideSearch || e.ctrlKey || e.metaKey || e.altKey || e.key.length !== 1) return
  const el = searchInput()
  if (!el || document.activeElement === el) return
  e.preventDefault()
  e.stopPropagation()
  searchText.value += e.key
  el.focus()
}

const onMenuChange = async (open: boolean): Promise<void> => {
  if (open) {
    pinned.value = compactActive.value ? selectedValues(props.modelValue) : []
    searchText.value = ''
    onQuery('', true)
    if (!props.hideSearch) {
      document.addEventListener('keydown', onDocumentKeydown, true)
      focusSearch()
    }
  } else {
    clearTimeout(timer)
    document.removeEventListener('keydown', onDocumentKeydown, true)
  }
}
onBeforeUnmount(() => document.removeEventListener('keydown', onDocumentKeydown, true))

// ---- What the menu lists ----------------------------------------------------

const baseItems = computed<Item[]>(() => {
  if (isAsync.value) return remote.value ?? normalizeItems(props.items)
  const items = normalizeItems(props.items)
  const text = searchText.value.trim()
  return text ? items.filter((item) => matchesSearch(item, text)) : items
})

// Async mode: selected options missing from the results stay in the list handed
// to v-select (so its field still shows their title) but render no row.
const extras = computed<Item[]>(() => {
  if (!isAsync.value) return []
  remember(normalizeItems(props.initialItems))
  remember(normalizeItems(props.items))
  const present = new Set(baseItems.value.map(getItemValue))
  const model = props.modelValue
  const keys = (Array.isArray(model) ? model : model == null ? [] : [model]).map((m) =>
    typeof m === 'object' && m !== null ? getItemValue(m) : String(m),
  )
  const out: Item[] = []
  for (const k of keys) {
    const it = known.get(k)
    if (it && !present.has(k)) out.push(it)
  }
  return out
})
const hiddenKeys = computed<Set<string>>(() => new Set(extras.value.map(getItemValue)))
const isHidden = (item: unknown): boolean => hiddenKeys.value.has(getItemValue(item))

const menuItems = computed<Item[]>(() => {
  const base = compactActive.value ? orderSelectedFirst(baseItems.value, pinned.value, rawItemValue) : baseItems.value
  return extras.value.length ? [...base, ...extras.value] : base
})

// ---- Exposed API ------------------------------------------------------------

const reset = (): void => {
  searchText.value = ''
  selectEl.value?.reset()
}

defineExpose({ reset })
</script>
