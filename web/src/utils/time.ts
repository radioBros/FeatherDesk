// SPDX-License-Identifier: AGPL-3.0-only

const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 365 * 24 * 3600],
  ['month', 30 * 24 * 3600],
  ['week', 7 * 24 * 3600],
  ['day', 24 * 3600],
  ['hour', 3600],
  ['minute', 60],
]

/** "3 minutes ago" in the given locale; under 45 s returns null (caller shows "just now"). */
export function relativeTime(date: Date, now: Date, locale: string): string | null {
  const diff = (date.getTime() - now.getTime()) / 1000
  const abs = Math.abs(diff)
  if (abs < 45) return null
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' })
  for (const [unit, secs] of UNITS) {
    if (abs >= secs) return rtf.format(Math.round(diff / secs), unit)
  }
  return rtf.format(Math.round(diff / 60), 'minute')
}

export function absoluteTime(date: Date, locale: string): string {
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(date)
}
