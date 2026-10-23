// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Slack route triggers (OpenAPI `SlackTrigger`), grouped for the rule form,
 * and the one-click presets.
 */
import {
  mdiAccountArrowRightOutline,
  mdiAlarmLight,
  mdiArrowUpBoldBoxOutline,
  mdiFlagOutline,
  mdiLockOutline,
  mdiMessageReplyTextOutline,
  mdiSwapHorizontal,
  mdiTicketOutline,
  mdiTimerSandComplete,
  mdiStarOutline,
  mdiFaceAgent,
} from '@mdi/js'
import type { components } from '@/api/schema'

export type Trigger = components['schemas']['SlackTrigger']

export interface TriggerDef {
  value: Trigger
  key: string // agent.integrations.slack.triggers.<key>
  icon: string
}

export const TRIGGER_GROUPS: { key: 'tickets' | 'conversation' | 'sla'; items: TriggerDef[] }[] = [
  {
    key: 'tickets',
    items: [
      { value: 'ticket.created', key: 'created', icon: mdiTicketOutline },
      { value: 'ticket.status_changed', key: 'status', icon: mdiSwapHorizontal },
      { value: 'ticket.assigned', key: 'assigned', icon: mdiAccountArrowRightOutline },
      { value: 'ticket.priority_changed', key: 'priority', icon: mdiFlagOutline },
      { value: 'ticket.escalated', key: 'escalated', icon: mdiArrowUpBoldBoxOutline },
    ],
  },
  {
    key: 'conversation',
    items: [
      { value: 'message.customer', key: 'customer', icon: mdiMessageReplyTextOutline },
      { value: 'message.staff', key: 'staff', icon: mdiFaceAgent },
      { value: 'note.created', key: 'note', icon: mdiLockOutline },
    ],
  },
  {
    key: 'sla',
    items: [
      { value: 'sla.warning', key: 'slaWarning', icon: mdiTimerSandComplete },
      { value: 'sla.breached', key: 'slaBreach', icon: mdiAlarmLight },
      { value: 'ticket.csat', key: 'csat', icon: mdiStarOutline },
    ],
  },
]

export const TRIGGERS: TriggerDef[] = TRIGGER_GROUPS.flatMap((g) => g.items)

export const triggerDef = (v: string): TriggerDef | undefined => TRIGGERS.find((t) => t.value === v)

/** Presets: each replaces the selection (internal notes only when allowed). */
export const PRESETS: { key: string; events: Trigger[] }[] = [
  { key: 'newTickets', events: ['ticket.created'] },
  { key: 'updates', events: ['ticket.created', 'ticket.status_changed', 'ticket.assigned', 'ticket.priority_changed', 'ticket.escalated'] },
  { key: 'conversation', events: ['ticket.created', 'message.customer', 'message.staff'] },
  { key: 'sla', events: ['sla.warning', 'sla.breached', 'ticket.escalated'] },
  { key: 'everything', events: TRIGGERS.map((t) => t.value) },
]

/** The events of a preset, without internal notes unless the route includes them. */
export function presetEvents(key: string, internal: boolean): Trigger[] {
  const p = PRESETS.find((x) => x.key === key)
  return (p?.events ?? []).filter((e) => internal || e !== 'note.created')
}

export const PRIORITIES = ['urgent', 'high', 'normal', 'low'] as const
