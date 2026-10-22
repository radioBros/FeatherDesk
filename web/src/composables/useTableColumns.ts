// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Column visibility for a table, chosen per user and per table.
 *
 * Every table declares its columns; the ones marked `locked` (the main column,
 * the ID/ref column) are always shown and cannot be switched off. The selection
 * checkbox column of a data table is not a column here: it is never offered.
 *
 * The choice is stored as overrides on top of the defaults, in localStorage
 * (there is no preferences endpoint), under `fd.v1.cols.<user>.<table>`.
 * Storage may be unavailable or corrupt: every access is guarded.
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
}

type Overrides = Record<string, boolean>

export const COLUMNS_PREFIX = 'fd.v1.cols.'

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
  try {
    if (Object.keys(o).length) localStorage.setItem(storageKey, JSON.stringify(o))
    else localStorage.removeItem(storageKey)
  } catch {
    /* private mode: the choice lasts for this page view only */
  }
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
  const mobile = opts.mobile ?? isMobileViewport
  const isMobile = ref(mobile())
  const overrides = ref<Overrides>(readOverrides(storageKey.value))
  watch(storageKey, (k) => (overrides.value = readOverrides(k)))

  const all = computed(() => toValue(columns))
  const byKey = computed(() => new Map(all.value.map((c) => [c.key, c])))

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
  const isDefault = computed(() => Object.keys(overrides.value).length === 0)

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
  function reset() {
    overrides.value = {}
    persist()
  }
  /** Re-evaluates the viewport (call from a resize handler); stored choices always win. */
  function refreshViewport() {
    isMobile.value = mobile()
  }

  return { all, toggleable, show, visibleKeys, count, isDefault, set, toggle, reset, refreshViewport, storageKey }
}

export type TableColumns = ReturnType<typeof useTableColumns>
