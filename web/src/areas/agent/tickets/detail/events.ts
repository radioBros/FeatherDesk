// SPDX-License-Identifier: AGPL-3.0-only

/** Timeline labels and details for `ticket_events` (events spec §1, FE-022). */
import { t } from '@/i18n'
import { useConfigStore } from '@/stores/config'
import type { components } from '@/api/schema'

type S = components['schemas']
const KNOWN = new Set<string>([
  'ticket.created', 'ticket.status_changed', 'ticket.priority_changed', 'ticket.assigned', 'ticket.team_changed',
  'ticket.escalated', 'ticket.deescalated', 'ticket.category_changed', 'ticket.tags_changed', 'ticket.fields_changed',
  'message.created', 'note.created', 'sla.warning', 'sla.breached', 'ticket.csat', 'ticket.followup_created',
  'ticket.merged', 'ticket.split', 'ticket.linked', 'ticket.unlinked', 'ticket.children_resolved',
])

export function eventLabel(type: string): string {
  if (type === AUTOMATION) return t('automation.timeline.applied')
  return KNOWN.has(type) ? t(`agent.ticket.events.${type.replace('.', '_')}`) : t('agent.ticket.events.unknown', { type })
}

/** D-19 A (FE-066): a rule applied; written by the system actor `automation`. */
export const AUTOMATION = 'ticket.automation_applied'

/** Actor line of a timeline entry: rule-made changes read "Automation", other actor-less ones "System". */
export function eventActor(e: Pick<S['TicketEvent'], 'type' | 'actor'>): string {
  if (e.actor?.name) return e.actor.name
  return e.type === AUTOMATION || e.actor?.id === 'automation' ? t('automation.timeline.actor') : t('agent.ticket.timeline.system')
}

const str = (v: unknown) => (v === null || v === undefined || v === '' ? '—' : String(v))

/** One line of detail per event type, from the event's `data`. */
export function eventDetail(e: Pick<S['TicketEvent'], 'type' | 'data'>): string {
  const d = (e.data ?? {}) as Record<string, unknown>
  const tier = (v: unknown) => (v === undefined || v === null ? '—' : useConfigStore().tierLabel(v as number))
  switch (e.type) {
    case 'ticket.status_changed':
      return `${d.from ? t(`status.${d.from}`) : '—'} → ${d.to ? t(`status.${d.to}`) : '—'}`
    case 'ticket.priority_changed':
      return `${d.from ? t(`priority.${d.from}`) : '—'} → ${d.to ? t(`priority.${d.to}`) : '—'}`
    case 'ticket.escalated':
    case 'ticket.deescalated': {
      const base = `${tier(d.from_tier ?? d.from)} → ${tier(d.to_tier ?? d.to)}`
      return d.reason ? `${base} · ${t('agent.ticket.events.reason', { reason: String(d.reason) })}` : base
    }
    case 'ticket.assigned': {
      const to = (d.to as { name?: string } | null | undefined)?.name ?? (d.assignee as { name?: string } | undefined)?.name
      return to ? to : t('agent.ticket.events.unassigned')
    }
    case 'ticket.team_changed':
      return `${str((d.from as { name?: string })?.name ?? d.from_name)} → ${str((d.to as { name?: string })?.name ?? d.to_name)}`
    case 'ticket.tags_changed':
      return Array.isArray(d.tags) ? (d.tags as string[]).join(', ') : ''
    case 'ticket.merged':
    case 'ticket.split':
    case 'ticket.linked':
    case 'ticket.unlinked':
    case 'ticket.followup_created':
      return d.number || d.other_number ? `#${d.number ?? d.other_number}` : ''
    case AUTOMATION: {
      const actions = Array.isArray(d.actions) ? (d.actions as { type?: string; outcome?: string; detail?: string }[]) : []
      const parts = actions.map((a) => {
        const label = a.type ? t(`automation.actions.${a.type}`) : '?'
        return a.outcome && a.outcome !== 'applied' ? `${label} (${t(`automation.outcomes.${a.outcome}`)}${a.detail ? `: ${a.detail}` : ''})` : label
      })
      const head = d.rule_name ? t('automation.timeline.rule', { name: String(d.rule_name) }) : ''
      const tail = d.truncated ? ` · ${t('automation.timeline.truncated')}` : ''
      return [head, parts.join(', ')].filter(Boolean).join(': ') + tail
    }
    case 'ticket.csat':
      return d.score !== undefined ? `${d.score}/5` : ''
    default:
      return ''
  }
}
