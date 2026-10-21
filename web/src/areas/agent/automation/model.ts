// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Automation vocabulary for the rule builder (D-19 A, FE-063): which operators
 * each condition field takes (the `AutomationOperator` description of
 * api/openapi.yaml), the input each value needs, and the editable row models
 * that map to and from `AutomationCondition` / `AutomationAction`.
 */
import type { components } from '@/api/schema'

type S = components['schemas']
export type Field = S['AutomationConditionField']
export type Op = S['AutomationOperator']
export type ActionType = S['AutomationActionType']
export type RuleEvent = S['AutomationEvent']

export const EVENTS: RuleEvent[] = ['ticket.created', 'message.created', 'ticket.updated', 'time']
export const TIME_FIELDS: Field[] = ['hours_since_update', 'hours_since_status']
export const FIELDS: Field[] = [
  'subject', 'body', 'channel', 'priority', 'status', 'tier', 'category_id', 'team_id', 'organization_id',
  'requester_domain', 'tag', 'custom_field', 'business_hours', 'hours_since_update', 'hours_since_status',
]
export const ACTIONS: ActionType[] = [
  'set_priority', 'set_team', 'set_category', 'set_status', 'set_tier', 'add_tag', 'remove_tag', 'assign',
  'add_note', 'send_reply', 'escalate', 'add_watcher',
]
export const CHANNELS: S['Channel'][] = ['portal', 'agent', 'api', 'email']
export const PRIORITIES: S['Priority'][] = ['low', 'normal', 'high', 'urgent']
export const STATUSES: S['TicketStatus'][] = ['new', 'open', 'pending', 'on_hold', 'resolved', 'closed']
export const TIERS = [1, 2, 3] as const
export const MAX_CONDITIONS = 20
export const MAX_ACTIONS = 10

const OPS: Record<Field, Op[]> = {
  subject: ['contains', 'not_contains', 'regex'],
  body: ['contains', 'not_contains', 'regex'],
  channel: ['is', 'is_not'],
  priority: ['is', 'is_not'],
  status: ['is', 'is_not'],
  tier: ['is', 'is_not', 'gte', 'lte'],
  category_id: ['is', 'is_not'],
  team_id: ['is', 'is_not'],
  organization_id: ['is', 'is_not'],
  requester_domain: ['is', 'is_not'],
  tag: ['present', 'absent'],
  custom_field: ['equals', 'contains'],
  business_hours: ['is'],
  hours_since_update: ['gte'],
  hours_since_status: ['gte'],
}

/** Operators allowed for a field. */
export const opsFor = (f: Field): Op[] => OPS[f]

/** Condition fields offered for an event: the hours-since fields only on time rules. */
export const fieldsFor = (e: RuleEvent): Field[] => (e === 'time' ? FIELDS : FIELDS.filter((f) => !TIME_FIELDS.includes(f)))

/** How the value of a condition is edited. */
export type ValueKind = 'text' | 'multi' | 'tier' | 'domains' | 'tag' | 'bool' | 'hours'
export function valueKind(f: Field): ValueKind {
  switch (f) {
    case 'subject':
    case 'body':
    case 'custom_field':
      return 'text'
    case 'channel':
    case 'priority':
    case 'status':
    case 'category_id':
    case 'team_id':
    case 'organization_id':
      return 'multi'
    case 'tier':
      return 'tier'
    case 'requester_domain':
      return 'domains'
    case 'tag':
      return 'tag'
    case 'business_hours':
      return 'bool'
    default:
      return 'hours'
  }
}

/** One editable condition row. `value` holds the shape its `valueKind` needs. */
export interface ConditionRow {
  field: Field
  op: Op
  key: string
  value: string | number | boolean | string[] | null
}

function defaultValue(f: Field): ConditionRow['value'] {
  switch (valueKind(f)) {
    case 'multi':
    case 'domains':
      return []
    case 'bool':
      return true
    case 'tier':
    case 'hours':
      return null
    default:
      return ''
  }
}

export function newCondition(f: Field = 'subject'): ConditionRow {
  return { field: f, op: opsFor(f)[0]!, key: '', value: defaultValue(f) }
}

/** Keeps a row consistent after its field changed (first allowed operator, empty value). */
export function resetCondition(row: ConditionRow): void {
  row.op = opsFor(row.field)[0]!
  row.key = ''
  row.value = defaultValue(row.field)
}

export function conditionFromApi(c: S['AutomationCondition']): ConditionRow {
  const row = newCondition(c.field)
  row.op = c.op
  row.key = c.key ?? ''
  const kind = valueKind(c.field)
  const v = c.value
  if (kind === 'multi' || kind === 'domains') row.value = Array.isArray(v) ? v : v === undefined || v === null || v === '' ? [] : [String(v)]
  else if (kind === 'tier' || kind === 'hours') row.value = v === undefined || v === '' ? null : Number(v)
  else if (kind === 'bool') row.value = v === true || v === 'true'
  else row.value = v === undefined ? '' : String(v)
  return row
}

export function conditionToApi(r: ConditionRow): S['AutomationCondition'] {
  const out: S['AutomationCondition'] = { field: r.field, op: r.op }
  if (r.field === 'custom_field') out.key = r.key
  const kind = valueKind(r.field)
  if (kind === 'tier' || kind === 'hours') out.value = Number(r.value)
  else if (kind === 'text' || kind === 'tag') out.value = String(r.value ?? '').trim()
  else out.value = r.value as S['AutomationCondition']['value']
  return out
}

/** One editable action row. `assign` keeps its mode separately: a strategy or a specific agent. */
export interface ActionRow {
  type: ActionType
  value: string | number | null
  assignMode: 'round_robin' | 'least_busy' | 'agent'
  user_id: string | null
  body_md: string
  canned_response_id: string | null
}

export function newAction(type: ActionType = 'add_tag'): ActionRow {
  return { type, value: type === 'set_tier' ? null : '', assignMode: 'round_robin', user_id: null, body_md: '', canned_response_id: null }
}

export function resetAction(row: ActionRow): void {
  Object.assign(row, newAction(row.type))
}

export function actionFromApi(a: S['AutomationAction']): ActionRow {
  const row = newAction(a.type)
  row.value = a.value ?? (a.type === 'set_tier' ? null : '')
  if (a.type === 'set_tier' && row.value !== null) row.value = Number(row.value)
  row.user_id = a.user_id ?? null
  row.body_md = a.body_md ?? ''
  row.canned_response_id = a.canned_response_id ?? null
  if (a.type === 'assign') row.assignMode = a.user_id ? 'agent' : (a.strategy ?? 'round_robin')
  return row
}

export function actionToApi(r: ActionRow): S['AutomationAction'] {
  const out: S['AutomationAction'] = { type: r.type }
  switch (r.type) {
    case 'set_priority':
    case 'set_team':
    case 'set_category':
    case 'set_status':
      out.value = String(r.value ?? '')
      break
    case 'add_tag':
    case 'remove_tag':
      out.value = String(r.value ?? '').trim().toLowerCase()
      break
    case 'set_tier':
      out.value = Number(r.value)
      break
    case 'assign':
      if (r.assignMode === 'agent') out.user_id = r.user_id ?? undefined
      else out.strategy = r.assignMode
      break
    case 'add_watcher':
      out.user_id = r.user_id ?? undefined
      break
    case 'add_note':
      out.body_md = r.body_md
      break
    case 'send_reply':
      out.canned_response_id = r.canned_response_id ?? undefined
      break
  }
  return out
}

/** The parameter input an action needs. */
export type ActionParam = 'priority' | 'team' | 'category' | 'status' | 'tier' | 'tag' | 'assign' | 'note' | 'canned' | 'staff' | 'none'
export function actionParam(type: ActionType): ActionParam {
  switch (type) {
    case 'set_priority':
      return 'priority'
    case 'set_team':
      return 'team'
    case 'set_category':
      return 'category'
    case 'set_status':
      return 'status'
    case 'set_tier':
      return 'tier'
    case 'add_tag':
    case 'remove_tag':
      return 'tag'
    case 'assign':
      return 'assign'
    case 'add_note':
      return 'note'
    case 'send_reply':
      return 'canned'
    case 'add_watcher':
      return 'staff'
    default:
      return 'none'
  }
}
