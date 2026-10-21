// SPDX-License-Identifier: AGPL-3.0-only

import StaffPage from './StaffPage.vue'
import StaffForm from './StaffForm.vue'
import { settleConfirm } from '@/composables/useConfirm'
import { mountAgent, fx, json, byTestId, click, typeInto } from '../../testing'
import type { AgentHarness } from '../../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const staff = (extra = {}) =>
  fx.user({ id: 's-1', kind: 'staff', role: 'agent', tier: 2, name: 'Sam Staff', email: 'sam@x.test', team_ids: ['team-1'], ...extra })

function select(h: AgentHarness, testid: string) {
  return h.wrapper.findAllComponents({ name: 'VSelect' }).find((c) => c.attributes('data-testid') === testid)!
}

describe('StaffPage (FE-027)', () => {
  it('lists staff with tier label, teams and the SSO/local badge', async () => {
    const h = await mountAgent(StaffPage, {
      me: fx.me('admin'),
      config: { tier_labels: { '1': 'Front desk', '2': 'Specialist', '3': 'Engineering' } },
      api: {
        'GET /admin/staff': fx.page([staff({ idp_managed: true }), staff({ id: 's-2', name: 'Lou Local', tier: 1, idp_managed: false })]),
        'GET /agent/teams': [fx.team()],
      },
    })
    await h.flush()
    const rows = h.wrapper.findAll('[data-testid="staff-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.text()).toContain('Specialist')
    expect(rows[0]!.text()).toContain('Support')
    expect(rows[0]!.find('[data-testid="staff-source"]').text()).toBe('SSO')
    expect(rows[1]!.find('[data-testid="staff-source"]').text()).toBe('Local')
  })

  it('creates a local staff member', async () => {
    let body: any = null
    const h = await mountAgent(StaffPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/staff': fx.page([]),
        'GET /agent/teams': [fx.team()],
        'POST /admin/staff': (_u, init) => {
          body = init.body
          return json(staff(), 201)
        },
      },
    })
    await click(byTestId('staff-new'))
    await typeInto(byTestId('staff-name'), 'Sam Staff')
    await typeInto(byTestId('staff-email'), 'sam@x.test')
    await typeInto(byTestId('staff-password'), 'a-long-password-1')
    await click(byTestId('staff-save'))
    await h.flush()
    expect(body).toMatchObject({ name: 'Sam Staff', email: 'sam@x.test', role: 'agent', tier: 1, password: 'a-long-password-1' })
    expect(h.calls('GET /admin/staff')).toHaveLength(2)
  })
})

describe('StaffForm (FE-027)', () => {
  it('an empty submit sends nothing', async () => {
    const h = await mountAgent(StaffForm, { props: { teams: [] } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })

  it('idp_managed: role and tier are disabled and omitted from the PATCH body', async () => {
    const h = await mountAgent(StaffForm, {
      props: { staff: staff({ idp_managed: true }), teams: [fx.team()] },
      api: { 'PATCH /admin/staff/s-1': staff({ idp_managed: true }) },
    })
    expect(select(h, 'staff-role').props('disabled')).toBe(true)
    expect(select(h, 'staff-tier').props('disabled')).toBe(true)
    expect(h.wrapper.find('[data-testid="staff-idp-hint"]').exists()).toBe(true)
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    const body = h.calls('PATCH /admin/staff/s-1')[0]!.body as Record<string, unknown>
    expect(body).not.toHaveProperty('role')
    expect(body).not.toHaveProperty('tier')
    expect(body).toMatchObject({ name: 'Sam Staff', team_ids: ['team-1'], status: 'active' })
  })

  it('a local user PATCH carries role and tier', async () => {
    const h = await mountAgent(StaffForm, {
      props: { staff: staff(), teams: [] },
      api: { 'PATCH /admin/staff/s-1': staff() },
    })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.calls('PATCH /admin/staff/s-1')[0]!.body).toMatchObject({ role: 'agent', tier: 2 })
  })

  it('disabling asks for confirmation; cancelling sends nothing', async () => {
    const h = await mountAgent(StaffForm, {
      props: { staff: staff(), teams: [] },
      api: { 'PATCH /admin/staff/s-1': staff({ status: 'disabled' }) },
    })
    select(h, 'staff-status').vm.$emit('update:modelValue', 'disabled')
    await h.flush()
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    settleConfirm(false)
    await h.flush()
    expect(h.calls()).toHaveLength(0)

    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    settleConfirm(true)
    await h.flush()
    expect(h.calls('PATCH /admin/staff/s-1')[0]!.body).toMatchObject({ status: 'disabled' })
  })
})
