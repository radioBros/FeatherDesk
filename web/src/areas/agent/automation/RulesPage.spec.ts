// SPDX-License-Identifier: AGPL-3.0-only

import RulesPage from './RulesPage.vue'
import DryRunForm from './DryRunForm.vue'
import { mountAgent, fx, json, byTestId, allByTestId, typeInto, click } from '../testing'
import { settleConfirm } from '@/composables/useConfirm'
import type { components } from '@/api/schema'

type S = components['schemas']

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const mk = (id: string, position: number, extra: Partial<S['AutomationRule']> = {}): S['AutomationRule'] => ({
  id,
  name: `Rule ${id}`,
  event: 'ticket.created',
  match: 'all',
  conditions: [],
  actions: [{ type: 'add_tag', value: 'x' }],
  enabled: true,
  stop_processing: false,
  position,
  created_at: fx.iso,
  updated_at: fx.iso,
  ...extra,
})

describe('RulesPage (FE-062)', () => {
  it('lists the rules in position order with event, counts and the stop marker', async () => {
    const h = await mountAgent(RulesPage, {
      me: fx.me('supervisor'),
      api: { 'GET /admin/automation/rules': [mk('b', 2, { event: 'time' }), mk('a', 1, { stop_processing: true })] },
    })
    await h.flush()
    const rows = h.wrapper.findAll('[data-testid="rule-row"]')
    expect(rows.map((r) => r.text().includes('Rule a'))).toEqual([true, false])
    expect(rows[0]!.find('[data-testid="rule-stop-icon"]').exists()).toBe(true)
    expect(rows[1]!.text()).toContain('Time-based (hourly)')
    expect(rows[0]!.text()).toContain('0 conditions, 1 actions')
  })

  it('moving a rule down posts every id in the new order', async () => {
    const h = await mountAgent(RulesPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/automation/rules': [mk('a', 1), mk('b', 2), mk('c', 3)],
        'POST /admin/automation/rules/reorder': (_u, init) => json((init.body as { ids: string[] }).ids.map((id, i) => mk(id, i + 1))),
      },
    })
    await click(allByTestId('rule-down')[0]!)
    expect(h.calls('POST /admin/automation/rules/reorder')[0]!.body).toEqual({ ids: ['b', 'a', 'c'] })
    expect(h.wrapper.findAll('[data-testid="rule-row"]')[0]!.text()).toContain('Rule b')
  })

  it('the switch PATCHes enabled only', async () => {
    const h = await mountAgent(RulesPage, {
      me: fx.me('admin'),
      api: { 'GET /admin/automation/rules': [mk('a', 1)], 'PATCH /admin/automation/rules/a': json(mk('a', 1, { enabled: false })) },
    })
    h.wrapper.findComponent({ name: 'VSwitch' }).vm.$emit('update:modelValue', false)
    await h.flush()
    expect(h.calls('PATCH /admin/automation/rules/a')[0]!.body).toEqual({ enabled: false })
  })

  it('deletes after confirmation', async () => {
    const h = await mountAgent(RulesPage, {
      me: fx.me('admin'),
      api: { 'GET /admin/automation/rules': [mk('a', 1)], 'DELETE /admin/automation/rules/a': () => new Response(null, { status: 204 }) },
    })
    await click(byTestId('rule-delete'))
    settleConfirm(true)
    await h.flush()
    expect(h.calls('DELETE /admin/automation/rules/a')).toHaveLength(1)
  })

  it('opening the builder loads the reference data once', async () => {
    const h = await mountAgent(RulesPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/automation/rules': [],
        'GET /agent/teams': [fx.team()],
        'GET /agent/categories': [],
        'GET /agent/staff': [],
        'GET /agent/custom-fields': [],
        'GET /admin/canned-responses': [],
        'GET /agent/organizations': fx.page([]),
      },
    })
    await click(byTestId('rule-new'))
    await h.flush()
    expect(byTestId('rule-form')).not.toBeNull()
    expect(h.calls('GET /admin/canned-responses')).toHaveLength(1)
    expect(h.calls('GET /agent/organizations')).toHaveLength(1)
  })
})

describe('DryRunForm (FE-063)', () => {
  it('an empty run sends nothing', async () => {
    const h = await mountAgent(DryRunForm, { props: { rule: mk('a', 1) } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })

  it('resolves the ticket number, posts the dry run and shows conditions and outcomes', async () => {
    const result: S['AutomationDryRun'] = {
      rule_id: 'a',
      ticket_id: 't-1',
      matched: true,
      conditions: [{ index: 0, field: 'subject', matched: true }],
      actions: [{ type: 'assign', outcome: 'skipped', detail: 'no eligible agent' }],
    }
    const h = await mountAgent(DryRunForm, {
      props: { rule: mk('a', 1) },
      api: { 'GET /agent/tickets/by-number/1042': fx.ticket(), 'POST /admin/automation/rules/a/dry-run': json(result) },
    })
    await typeInto(byTestId('dry-run-number'), '1042')
    await click(byTestId('dry-run-submit'))
    expect(h.calls('POST /admin/automation/rules/a/dry-run')[0]!.body).toEqual({ ticket_id: 't-1' })
    expect(byTestId('dry-run-verdict')?.textContent).toContain('The rule matches this ticket.')
    expect(allByTestId('dry-run-condition')).toHaveLength(1)
    expect(byTestId('dry-run-action')?.textContent).toContain('skipped · no eligible agent')
  })

  it('an unknown ticket number shows the error and posts nothing', async () => {
    const h = await mountAgent(DryRunForm, { props: { rule: mk('a', 1) } })
    await typeInto(byTestId('dry-run-number'), '99')
    await click(byTestId('dry-run-submit'))
    expect(h.calls('POST /admin/automation/rules/a/dry-run')).toHaveLength(0)
    expect(h.wrapper.find('.v-alert').exists()).toBe(true)
  })
})
