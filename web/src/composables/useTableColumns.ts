// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Column visibility, order and widths for a table, chosen per user and per table.
 *
 * Every table declares its columns; the ones marked `locked` (the main column,
 * the ID/ref column) are always shown and cannot be switched off. The selection
 * checkbox column of a data table is not a column here: it is never offered.
 * Every column (locked ones too) can be moved in the Columns menu and resized
 * by its header edge (DataTable, ColumnResizer).
 *
 * The choices are stored on top of the defaults, in localStorage (there is no
 * preferences endpoint): visibility under `fd.v1.cols.<user>.<table>`, order
 * under `fd.v1.colorder.<user>.<table>`, widths in px under
 * `fd.v1.colwidth.<user>.<table>`. Widths also cover columns that are not
 * declared (the selection and actions columns), keyed `:<n>` by their position
 * among them. Storage may be unavailable or corrupt: every access is guarded.
 */
import { computed, ref, toValue, watch, type MaybeRefOrGetter } from 'vue'
import { useSessionStore } from '@/stores/session'

export interface ColumnDef {
  key: string
  /** Translated label shown in the menu. */
  label: string
  /** Main/ID column: always shown, cannot be toggled. */
  locked?: boolean
  /** Hidden by default on a phone. */
  lowPriority?: boolean
  /** Hidden by default everywhere (e.g. optional custom-field columns). */
  defaultHidden?: boolean
  /**
   * DataTable: the API `sort` values that order the list by this column. A
   * server-sorted table (cursor lists) offers header sorting only on columns
   * that have them.
   */
  sort?: { asc: string; desc: string }
  /**
   * DataTable, client-sorted tables (every row loaded): what the column sorts
   * by — the item field named like the key by default; `false` = not sortable.
   */
  sortValue?: ((item: never) => unknown) | false
  align?: 'start' | 'center' | 'end'
  /** Width in px before the user resizes anything. */
  width?: number
}

type Overrides = Record<string, boolean>

export const COLUMNS_PREFIX = 'fd.v1.cols.'
export const ORDER_PREFIX = 'fd.v1.colorder.'
export const WIDTH_PREFIX = 'fd.v1.colwidth.'
/** Narrowest a column can be resized to, in px. */
export const MIN_COLUMN_WIDTH = 48

export function isMobileViewport(): boolean {
  try {
    return typeof window !== 'undefined' && !!window.matchMedia?.('(max-width: 959.98px)').matches
  } catch {
    return false
  }
}

export function readOverrides(storageKey: string): Overrides {
  try {
    const raw = localStorage.getItem(storageKey)
    const v = raw ? JSON.parse(raw) : null
    if (!v || typeof v !== 'object' || Array.isArray(v)) return {}
    const out: Overrides = {}
    for (const [k, on] of Object.entries(v)) if (typeof on === 'boolean') out[k] = on
    return out
  } catch {
    return {}
  }
}

function writeOverrides(storageKey: string, o: Overrides) {
  writeJSON(storageKey, Object.keys(o).length ? o : null)
}

function writeJSON(storageKey: string, v: unknown) {
  try {
    if (v === null) localStorage.removeItem(storageKey)
    else localStorage.setItem(storageKey, JSON.stringify(v))
  } catch {
    /* private mode: the choice lasts for this page view only */
  }
}

export function readOrder(storageKey: string): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(storageKey) ?? 'null')
    return Array.isArray(v) ? v.filter((k): k is string => typeof k === 'string') : []
  } catch {
    return []
  }
}

export function readWidths(storageKey: string): Record<string, number> {
  try {
    const v = JSON.parse(localStorage.getItem(storageKey) ?? 'null')
    if (!v || typeof v !== 'object' || Array.isArray(v)) return {}
    const out: Record<string, number> = {}
    for (const [k, w] of Object.entries(v)) if (typeof w === 'number' && Number.isFinite(w) && w > 0) out[k] = Math.max(MIN_COLUMN_WIDTH, Math.round(w))
    return out
  } catch {
    return {}
  }
}

/**
 * Applies a stored order to the declared keys. Stored keys the table no longer
 * declares are dropped; a declared key the order does not know yet (a new
 * custom field) goes right after its nearest declared predecessor.
 */
export function applyOrder(declared: string[], stored: string[]): string[] {
  if (!stored.length) return declared
  const known = new Set(declared)
  const out = stored.filter((k, i) => known.has(k) && stored.indexOf(k) === i)
  const placed = new Set(out)
  declared.forEach((k, i) => {
    if (placed.has(k)) return
    let at = 0
    for (let j = i - 1; j >= 0; j--) {
      const p = out.indexOf(declared[j]!)
      if (p >= 0) {
        at = p + 1
        break
      }
    }
    out.splice(at, 0, k)
    placed.add(k)
  })
  return out
}

export interface TableColumnsOptions {
  /** Overrides the phone detection (tests). */
  mobile?: () => boolean
  /** Overrides the user id used in the storage key (tests). */
  userId?: () => string | undefined
}

export function useTableColumns(tableId: string, columns: MaybeRefOrGetter<ColumnDef[]>, opts: TableColumnsOptions = {}) {
  const session = useSessionStore()
  const uid = () => opts.userId?.() ?? session.me?.id ?? 'anon'
  const storageKey = computed(() => `${COLUMNS_PREFIX}${uid()}.${tableId}`)
  const orderKey = computed(() => `${ORDER_PREFIX}${uid()}.${tableId}`)
  const widthKey = computed(() => `${WIDTH_PREFIX}${uid()}.${tableId}`)
  const mobile = opts.mobile ?? isMobileViewport
  const isMobile = ref(mobile())
  const overrides = ref<Overrides>(readOverrides(storageKey.value))
  const order = ref<string[]>(readOrder(orderKey.value))
  const widths = ref<Record<string, number>>(readWidths(widthKey.value))
  watch(storageKey, (k) => (overrides.value = readOverrides(k)))
  watch(orderKey, (k) => (order.value = readOrder(k)))
  watch(widthKey, (k) => (widths.value = readWidths(k)))

  const declared = computed(() => toValue(columns))
  const byKey = computed(() => new Map(declared.value.map((c) => [c.key, c])))
  /** Every declared column, in the user's order. */
  const all = computed(() => applyOrder(declared.value.map((c) => c.key), order.value).map((k) => byKey.value.get(k)!))
  /** Position of each declared column in the user's order. */
  const rank = computed(() => new Map(all.value.map((c, i) => [c.key, i])))

  function defaultShown(c: ColumnDef): boolean {
    if (c.locked) return true
    if (c.defaultHidden) return false
    return !(isMobile.value && c.lowPriority)
  }
  function isShown(c: ColumnDef): boolean {
    if (c.locked) return true
    return overrides.value[c.key] ?? defaultShown(c)
  }

  /** Whether a column renders. Keys the table did not declare stay visible. */
  function show(key: string): boolean {
    const c = byKey.value.get(key)
    return c ? isShown(c) : true
  }
  const visibleKeys = computed(() => all.value.filter(isShown).map((c) => c.key))
  /** Columns the user may toggle. */
  const toggleable = computed(() => all.value.filter((c) => !c.locked))
  /** Number of columns currently rendered, for `colspan` of full-width rows. */
  const count = computed(() => visibleKeys.value.length)
  const isDefault = computed(() => Object.keys(overrides.value).length === 0 && order.value.length === 0 && Object.keys(widths.value).length === 0)
  /** Whether the user has resized any column: the table then uses fixed widths. */
  const sized = computed(() => Object.keys(widths.value).length > 0)

  function persist() {
    writeOverrides(storageKey.value, overrides.value)
  }
  function set(key: string, on: boolean) {
    const c = byKey.value.get(key)
    if (!c || c.locked) return
    const next = { ...overrides.value }
    if (on === defaultShown(c)) delete next[key]
    else next[key] = on
    overrides.value = next
    persist()
  }
  function toggle(key: string) {
    const c = byKey.value.get(key)
    if (c) set(key, !isShown(c))
  }
  /** Moves a column to position `to` of the ordered list (all declared columns). */
  function move(key: string, to: number) {
    const keys = all.value.map((c) => c.key)
    const from = keys.indexOf(key)
    if (from < 0) return
    const target = Math.max(0, Math.min(keys.length - 1, to))
    if (target === from) return
    keys.splice(from, 1)
    keys.splice(target, 0, key)
    const isDeclared = keys.every((k, i) => k === declared.value[i]?.key)
    order.value = isDeclared ? [] : keys
    writeJSON(orderKey.value, order.value.length ? order.value : null)
  }
  /** Width in px of a column (or of an undeclared column `:<n>`), when the user set one. */
  function width(key: string): number | undefined {
    return widths.value[key]
  }
  /** Sets column widths in px; `save` false keeps a drag in progress out of storage. */
  function setWidths(next: Record<string, number>, save = true) {
    const merged = { ...widths.value }
    for (const [k, w] of Object.entries(next)) merged[k] = Math.max(MIN_COLUMN_WIDTH, Math.round(w))
    widths.value = merged
    if (save) saveWidths()
  }
  function saveWidths() {
    writeJSON(widthKey.value, Object.keys(widths.value).length ? widths.value : null)
  }
  function reset() {
    overrides.value = {}
    order.value = []
    widths.value = {}
    persist()
    writeJSON(orderKey.value, null)
    writeJSON(widthKey.value, null)
  }
  /** Re-evaluates the viewport (call from a resize handler); stored choices always win. */
  function refreshViewport() {
    isMobile.value = mobile()
  }

  return {
    all,
    rank,
    toggleable,
    show,
    visibleKeys,
    count,
    isDefault,
    sized,
    widths,
    width,
    set,
    toggle,
    move,
    setWidths,
    saveWidths,
    reset,
    refreshViewport,
    storageKey,
  }
}

export type TableColumns = ReturnType<typeof useTableColumns>
