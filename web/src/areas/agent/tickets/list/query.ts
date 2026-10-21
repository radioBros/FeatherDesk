// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Ticket-list filters ⇄ URL query ⇄ API query (FE-021, FE-054). Pure functions
 * so the mapping is unit-tested; the view keeps the URL as the single source
 * of truth (a reload restores the filters) and never filters results itself.
 */
import type { LocationQuery, LocationQueryRaw } from 'vue-router'

export type QueueView = 'mine' | 'unassigned' | 'triage' | 'tier' | 'all'

export interface ListFilters {
  view?: QueueView
  view_id?: string
  status: string[]
  priority: string[]
  tier?: string
  team_id?: string
  assignee?: string
  category_id?: string
  tag?: string
  q?: string
  breached?: boolean
  sort: string
  /** custom-field filters, `cf.<key>` → value (strings, comma-joined for lists) */
  cf: Record<string, string>
}

export const DEFAULT_SORT = 'attention'
export const SORTS = [
  { value: 'attention', key: 'attention' },
  { value: '-updated_at', key: 'updatedDesc' },
  { value: 'updated_at', key: 'updatedAsc' },
  { value: '-created_at', key: 'createdDesc' },
  { value: 'created_at', key: 'createdAsc' },
  { value: '-priority', key: 'priorityDesc' },
  { value: 'due', key: 'due' },
  { value: '-number', key: 'numberDesc' },
] as const

const VIEWS: QueueView[] = ['mine', 'unassigned', 'triage', 'tier', 'all']
const SIMPLE = ['tier', 'team_id', 'assignee', 'category_id', 'tag', 'q', 'view_id'] as const

const one = (v: unknown): string | undefined => {
  const x = Array.isArray(v) ? v[0] : v
  return typeof x === 'string' && x !== '' ? x : undefined
}
const list = (v: unknown): string[] => (one(v) ?? '').split(',').map((s) => s.trim()).filter(Boolean)

export function filtersFromQuery(q: LocationQuery): ListFilters {
  const view = one(q.view)
  const f: ListFilters = {
    view: VIEWS.includes(view as QueueView) ? (view as QueueView) : undefined,
    status: list(q.status),
    priority: list(q.priority),
    breached: one(q.breached) === 'true' ? true : undefined,
    sort: one(q.sort) ?? DEFAULT_SORT,
    cf: {},
  }
  for (const k of SIMPLE) {
    const v = one(q[k])
    if (v !== undefined) f[k] = v
  }
  for (const [k, v] of Object.entries(q)) {
    if (k.startsWith('cf.') && one(v) !== undefined) f.cf[k.slice(3)] = one(v)!
  }
  return f
}

/** The URL query for a filter set (empty values dropped, default sort omitted). */
export function toRouteQuery(f: ListFilters): LocationQueryRaw {
  const q: LocationQueryRaw = {}
  if (f.view) q.view = f.view
  if (f.status.length) q.status = f.status.join(',')
  if (f.priority.length) q.priority = f.priority.join(',')
  for (const k of SIMPLE) if (f[k]) q[k] = f[k]
  if (f.breached) q.breached = 'true'
  if (f.sort && f.sort !== DEFAULT_SORT) q.sort = f.sort
  for (const [k, v] of Object.entries(f.cf)) if (v !== '') q[`cf.${k}`] = v
  return q
}

/**
 * The `/agent/tickets` query string. Queue views map to API filters
 * (mine → assignee=me, unassigned → assignee=none, triage → team_id=none);
 * an explicit filter wins over the view's.
 */
export function toApiParams(f: ListFilters, cursor?: string | null, limit = 50): URLSearchParams {
  const p = new URLSearchParams()
  const fromView: Record<string, string> =
    f.view === 'mine' ? { assignee: 'me' } : f.view === 'unassigned' ? { assignee: 'none' } : f.view === 'triage' ? { team_id: 'none' } : {}
  const set = (k: string, v?: string) => {
    if (v !== undefined && v !== '') p.set(k, v)
  }
  if (f.status.length) set('status', f.status.join(','))
  if (f.priority.length) set('priority', f.priority.join(','))
  set('tier', f.tier)
  set('team_id', f.team_id ?? fromView.team_id)
  set('assignee', f.assignee ?? fromView.assignee)
  set('category_id', f.category_id)
  set('tag', f.tag)
  set('q', f.q)
  if (f.breached) set('breached', 'true')
  set('sort', f.sort || DEFAULT_SORT)
  for (const [k, v] of Object.entries(f.cf)) set(`cf.${k}`, v)
  set('limit', String(limit))
  if (cursor) set('cursor', cursor)
  return p
}

/** Serialises a typed custom-field filter value for the URL / API. */
export function cfToString(v: unknown): string {
  if (v === null || v === undefined) return ''
  if (Array.isArray(v)) return v.join(',')
  if (typeof v === 'boolean') return v ? 'true' : ''
  return String(v)
}

/** Parses a URL value back into the input's typed value. */
export function cfFromString(type: string, s: string | undefined): string | number | boolean | string[] | null {
  if (s === undefined || s === '') return type === 'multiselect' ? [] : type === 'checkbox' ? false : null
  if (type === 'multiselect') return s.split(',')
  if (type === 'checkbox') return s === 'true'
  if (type === 'number') return Number.isFinite(Number(s)) ? Number(s) : s
  return s
}
