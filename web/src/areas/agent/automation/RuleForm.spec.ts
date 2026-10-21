// SPDX-License-Identifier: AGPL-3.0-only

import RuleForm from './RuleForm.vue'
import { mountAgent, fx, json, byTestId, typeInto, click, type AgentHarness } from '../testing'
import type { components } from '@/api/schema'

type S = components['schemas']

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const refs = { teams: [fx.team()], categories: [fx.category()], staff: [fx.staffRef()], canned: [], customFields: [], organizations: [] }

/** The Vuetify select/autocomplete component carrying `data-testid` (index for repeated rows). */
function select(h: AgentHarness, testid: string, index = 0) {
  const all = [...h.wrapper.findAllComponents({ name: 'VSelect' }), ...h.wrapper.findAllComponents({ name: 'VAutocomplete' })].filter(
    (c) => c.attributes('data-testid') === testid,
  )
  const c = all[index]
  if (!c) throw new Error(`no select ${testid}[${index}]`)
  return c
}
const items = (h: AgentHarness, testid: string, index = 0) => (select(h, testid, index).props('items') as { value: unknown }[]).map((i) => i.value)
async function choose(h: AgentHarness, testid: string, value: unknown, index = 0) {
  select(h, testid, index).vm.$emit('update:modelValue', value)
  await h.flush()
}

const rule: S['AutomationRule'] = {
  id: 'r-1',
  name: 'VIP routing',
  event: 'ticket.created',
  match: 'any',
  conditions: [{ field: 'requester_domain', op: 'is', value: ['vip.example'] }],
  actions: [{ type: 'assign', strategy: 'least_busy' }, { type: 'add_tag', value: 'vip' }],
  enabled: true,
  stop_processing: true,
  project_id: null,
  position: 1,
  created_at: fx.iso,
  updated_at: fx.iso,
}

describe('RuleForm (FE-063, FE-005)', () => {
  it('an empty submit sends nothing and flags the name and the first action', async () => {
    const h = await mountAgent(RuleForm, { props: refs })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })

  it('offers only the operators of the chosen field', async () => {
    const h = await mountAgent(RuleForm, { props: refs })
    await click(byTestId('condition-add'))
    expect(items(h, 'condition-op')).toEqual(['contains', 'not_contains', 'regex'])
    await choose(h, 'condition-field', 'tier')
    expect(items(h, 'condition-op')).toEqual(['is', 'is_not', 'gte', 'lte'])
    await choose(h, 'condition-field', 'tag')
    expect(items(h, 'condition-op')).toEqual(['present', 'absent'])
    await choose(h, 'condition-field', 'business_hours')
    expect(items(h, 'condition-op')).toEqual(['is'])
  })

  it('hours-since fields exist only on time rules', async () => {
    const h = await mountAgent(RuleForm, { props: refs })
    await click(byTestId('condition-add'))
    expect(items(h, 'condition-field')).not.toContain('hours_since_update')
    await choose(h, 'rule-event', 'time')
    expect(items(h, 'condition-field')).toContain('hours_since_status')
    await choose(h, 'condition-field', 'hours_since_status')
    await choose(h, 'rule-event', 'ticket.updated')
    // a time-only condition is reset when the event is no longer `time`
    expect(select(h, 'condition-field').props('modelValue')).toBe('subject')
  })

  it('a regex condition rejects RE2-incompatible patterns client-side', async () => {
    const h = await mountAgent(RuleForm, { props: refs })
    await typeInto(byTestId('rule-name'), 'Regex')
    await typeInto(byTestId('action-value'), 'x')
    await click(byTestId('condition-add'))
    await choose(h, 'condition-op', 'regex')
    await typeInto(byTestId('condition-value'), '(?=lookahead)')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.calls()).toHaveLength(0)
    expect(h.wrapper.text()).toContain('This value does not have the required format')
  })

  it('creates a rule: subject condition, assign by round robin, add tag', async () => {
    const h = await mountAgent(RuleForm, {
      props: refs,
      api: { 'POST /admin/automation/rules': (_u, init) => json({ ...rule, ...init.body, id: 'r-9' }, 201) },
    })
    await typeInto(byTestId('rule-name'), 'Route outages')
    await click(byTestId('condition-add'))
    await typeInto(byTestId('condition-value'), 'outage')
    await choose(h, 'action-type', 'assign')
    await click(byTestId('action-add'))
    await choose(h, 'action-type', 'add_tag', 1)
    await typeInto(document.querySelectorAll('[data-testid="action-value"]')[0]!, 'Outage')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    // an uppercase tag is not a valid tag name: nothing is sent
    expect(h.calls('POST /admin/automation/rules')).toHaveLength(0)
    await typeInto(document.querySelectorAll('[data-testid="action-value"]')[0]!, 'outage')
    await click(byTestId('rule-save'))
    expect(h.calls('POST /admin/automation/rules')[0]!.body).toEqual({
      name: 'Route outages',
      event: 'ticket.created',
      match: 'all',
      enabled: true,
      stop_processing: false,
      conditions: [{ field: 'subject', op: 'contains', value: 'outage' }],
      actions: [{ type: 'assign', strategy: 'round_robin' }, { type: 'add_tag', value: 'outage' }],
    })
    expect(h.wrapper.emitted('saved')).toBeTruthy()
  })

  it('edits an existing rule with PATCH and keeps its conditions and actions', async () => {
    const h = await mountAgent(RuleForm, { props: { ...refs, rule }, api: { 'PATCH /admin/automation/rules/r-1': (_u, init) => json({ ...rule, ...init.body }) } })
    expect(select(h, 'action-assign-mode').props('modelValue')).toBe('least_busy')
    await click(byTestId('rule-save'))
    expect(h.calls('PATCH /admin/automation/rules/r-1')[0]!.body).toMatchObject({
      match: 'any',
      stop_processing: true,
      conditions: [{ field: 'requester_domain', op: 'is', value: ['vip.example'] }],
      actions: [{ type: 'assign', strategy: 'least_busy' }, { type: 'add_tag', value: 'vip' }],
    })
  })

  it('assign to a specific agent requires the agent', async () => {
    const h = await mountAgent(RuleForm, {
      props: refs,
      api: { 'POST /admin/automation/rules': (_u, init) => json({ ...rule, ...init.body }, 201) },
    })
    await typeInto(byTestId('rule-name'), 'To Bob')
    await choose(h, 'action-type', 'assign')
    await choose(h, 'action-assign-mode', 'agent')
    await click(byTestId('rule-save'))
    expect(h.calls()).toHaveLength(0)
    await choose(h, 'action-user', 'u-2')
    await click(byTestId('rule-save'))
    expect((h.calls('POST /admin/automation/rules')[0]!.body as S['AutomationRuleCreate']).actions).toEqual([{ type: 'assign', user_id: 'u-2' }])
  })

  it('maps server list errors (conditions/actions) to an alert', async () => {
    await mountAgent(RuleForm, {
      props: refs,
      api: {
        'POST /admin/automation/rules': () =>
          json({ type: 'https://simpletickets.dev/problems/validation', title: 'Invalid', status: 422, errors: [{ field: 'conditions', code: 'required', message: 'x' }] }, 422),
      },
    })
    await typeInto(byTestId('rule-name'), 'Time rule')
    await typeInto(byTestId('action-value'), 'stale')
    await click(byTestId('rule-save'))
    expect(byTestId('rule-list-errors')?.textContent).toContain('This field is required')
  })
})
