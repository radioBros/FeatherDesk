// SPDX-License-Identifier: AGPL-3.0-only

import type { components } from '@/api/schema'

export interface ReportFilter {
  from: string
  to: string
  team_id: string | null
  /** Project id (or null = all projects). */
  project?: string | null
  calendar_mode: components['schemas']['CalendarMode'] | null
}

/** "45 min", "3 h 20 min", "2 d 4 h" for a minute count (FE-043). */
export function formatMinutes(v: number | null | undefined, t: (k: string, p?: Record<string, unknown>) => string): string {
  if (v === null || v === undefined || !Number.isFinite(v)) return '—'
  const total = Math.round(v)
  if (total < 60) return t('agent.reports.dur.m', { m: total })
  if (total < 1440) return t('agent.reports.dur.hm', { h: Math.floor(total / 60), m: total % 60 })
  return t('agent.reports.dur.dh', { d: Math.floor(total / 1440), h: Math.floor((total % 1440) / 60) })
}
