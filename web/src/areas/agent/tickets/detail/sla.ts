// SPDX-License-Identifier: AGPL-3.0-only

/** SLA badge state (FE-039): met, paused, breached, warning at ≥ 80 % elapsed, ok. */
export type SlaState = 'met' | 'paused' | 'breached' | 'warning' | 'ok' | 'none'

export function slaState(opts: {
  start: string
  due?: string | null
  metAt?: string | null
  pausedAt?: string | null
  breached?: boolean
  now?: Date
}): SlaState {
  if (!opts.due) return 'none'
  const due = Date.parse(opts.due)
  if (opts.metAt) return Date.parse(opts.metAt) <= due ? 'met' : 'breached'
  if (opts.pausedAt) return 'paused'
  const now = (opts.now ?? new Date()).getTime()
  if (opts.breached || now > due) return 'breached'
  const start = Date.parse(opts.start)
  const span = due - start
  if (span > 0 && (now - start) / span >= 0.8) return 'warning'
  return 'ok'
}
