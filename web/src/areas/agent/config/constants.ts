// SPDX-License-Identifier: AGPL-3.0-only

import type { components } from '@/api/schema'

type S = components['schemas']

/** Every `EventType` a webhook can subscribe to (api/openapi.yaml). */
export const EVENT_TYPES: S['EventType'][] = [
  'ticket.created',
  'ticket.status_changed',
  'ticket.priority_changed',
  'ticket.assigned',
  'ticket.team_changed',
  'ticket.escalated',
  'ticket.deescalated',
  'ticket.category_changed',
  'ticket.tags_changed',
  'ticket.fields_changed',
  'message.created',
  'note.created',
  'sla.warning',
  'sla.breached',
  'ticket.csat',
  'ticket.followup_created',
  'ticket.merged',
  'ticket.split',
  'ticket.linked',
  'ticket.unlinked',
  'ticket.children_resolved',
]

export const PRIORITIES: S['Priority'][] = ['low', 'normal', 'high', 'urgent']

export const FIELD_TYPES: S['FieldType'][] = ['text', 'textarea', 'number', 'date', 'select', 'multiselect', 'checkbox']

export const WEEKDAYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'] as const
export type Weekday = (typeof WEEKDAYS)[number]
