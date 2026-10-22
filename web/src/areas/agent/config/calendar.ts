// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Client-side helpers for business calendars (FE-056): the IANA zone list
 * from `Intl` (no network) and a "next business minute" preview.
 */
import type { components } from '@/api/schema'
import { WEEKDAYS, type Weekday } from './constants'

type S = components['schemas']

export function timeZones(): string[] {
  try {
    const list = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.('timeZone')
    if (list?.length) return list.includes('UTC') ? list : ['UTC', ...list]
  } catch {
    /* old engines */
  }
  return ['UTC']
}

interface LocalParts {
  ymd: string
  weekday: Weekday
  minutes: number
}

const WD: Record<string, Weekday> = { Mon: 'mon', Tue: 'tue', Wed: 'wed', Thu: 'thu', Fri: 'fri', Sat: 'sat', Sun: 'sun' }

export function localParts(date: Date, timeZone: string): LocalParts {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    weekday: 'short',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).formatToParts(date)
  const get = (type: string) => parts.find((p) => p.type === type)?.value ?? ''
  const hour = Number(get('hour')) % 24
  return {
    ymd: `${get('year')}-${get('month')}-${get('day')}`,
    weekday: WD[get('weekday')] ?? 'mon',
    minutes: hour * 60 + Number(get('minute')),
  }
}

const toMin = (s: string) => {
  const [h, m] = s.split(':').map(Number)
  return (h ?? 0) * 60 + (m ?? 0)
}

/**
 * The first minute at or after `from` that falls inside the calendar's
 * business hours (holidays excluded), or null when the week has no hours.
 * Jumps interval-to-interval instead of minute-by-minute.
 */
export function nextBusinessMinute(
  cal: { timezone: string; week: S['CalendarCreate']['week']; holidays?: string[] },
  from: Date = new Date(),
): Date | null {
  const hasHours = WEEKDAYS.some((d) => (cal.week[d] ?? []).some((r) => toMin(r.end) > toMin(r.start)))
  if (!hasHours) return null
  const holidays = new Set(cal.holidays ?? [])
  let t = new Date(Math.ceil(from.getTime() / 60000) * 60000)
  for (let i = 0; i < 400; i++) {
    const p = localParts(t, cal.timezone)
    const ranges = holidays.has(p.ymd)
      ? []
      : (cal.week[p.weekday] ?? []).map((r) => [toMin(r.start), toMin(r.end)] as const).filter(([s, e]) => e > s).sort((a, b) => a[0] - b[0])
    if (ranges.some(([s, e]) => p.minutes >= s && p.minutes < e)) return t
    const later = ranges.find(([s]) => s > p.minutes)
    const jump = later ? later[0] - p.minutes : 1440 - p.minutes
    t = new Date(t.getTime() + Math.max(1, jump) * 60000)
  }
  return null
}
