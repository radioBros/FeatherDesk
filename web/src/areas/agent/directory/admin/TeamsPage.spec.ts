// SPDX-License-Identifier: AGPL-3.0-only

import TeamsPage from './TeamsPage.vue'
import TeamForm from './TeamForm.vue'
import TeamMembers from './TeamMembers.vue'
import { mountAgent, fx, json, byTestId, click } from '../../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const calendar = { id: 'cal-1', name: 'Office', timezone: 'Europe/Rome', holidays: [], week: {} }

describe('TeamsPage (FE-028)', () => {
  it('lists teams with notify mode and calendar', async () => {
    const h = await mountAgent(TeamsPage, {
      me: fx.me('supervisor'),
      api: {
        'GET /admin/teams': [fx.team({ notify_mode: 'both', notification_email: 'team@x.test', calendar_id: 'cal-1', member_count: 4 }), fx.team({ id: 'team-2', name: 'Billing' })],
        'GET /admin/calendars': [calendar],
      },
    })
    await h.flush()
    const rows = h.wrapper.findAll('[data-testid="team-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.text()).toContain('Members and mailbox')
    expect(rows[0]!.text()).toContain('team@x.test')
    expect(rows[0]!.text()).toContain('Office')
    expect(rows[1]!.text()).toContain('Always open (24×7)')
  })

  it('opens the members dialog and loads the staff picker', async () => {
    const h = await mountAgent(TeamsPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/teams': [fx.team()],
        'GET /admin/calendars': [],
        'GET /admin/staff': fx.page([]),
        'GET /admin/teams/team-1/members': [],
      },
    })
    await click(byTestId('team-members-open'))
    await h.flush()
    expect(byTestId('team-members')).not.toBeNull()
    expect(h.calls('GET /admin/staff')).toHaveLength(1)
  })
})

describe('TeamForm (FE-028, FE-056)', () => {
  it('an empty submit sends nothing', async () => {
    const h = await mountAgent(TeamForm, { props: { calendars: [] } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })
  it('notify_mode=mailbox with an empty email is rejected client-side', async () => {
    const h = await mountAgent(TeamForm, {
      props: { team: fx.team({ notify_mode: 'mailbox', notification_email: null }), calendars: [] },
    })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.find('[data-testid="team-email"]').text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })
  it('agents mode allows an empty email; the body carries notify_mode and calendar_id', async () => {
    let body: any = null
    const h = await mountAgent(TeamForm, {
      props: { team: fx.team({ calendar_id: 'cal-1' }), calendars: [calendar] },
      api: {
        'PATCH /admin/teams/team-1': (_u, init) => {
          body = init.body
          return json(fx.team())
        },
      },
    })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(body).toMatchObject({ name: 'Support', notify_mode: 'agents', notification_email: null, calendar_id: 'cal-1' })
  })
})

describe('TeamMembers (FE-028)', () => {
  it('lists members; IdP members are read-only; adding posts the user id', async () => {
    const h = await mountAgent(TeamMembers, {
      props: {
        team: fx.team(),
        staff: [fx.user({ id: 's-9', kind: 'staff', role: 'agent', name: 'Nina New', email: 'nina@x.test' })],
      },
      api: {
        'GET /admin/teams/team-1/members': [
          { user_id: 's-1', name: 'Sam', role: 'agent', tier: 1, source: 'local' },
          { user_id: 's-2', name: 'Ida', role: 'agent', tier: 2, source: 'idp' },
        ],
        'POST /admin/teams/team-1/members': json({ user_id: 's-9', name: 'Nina New', role: 'agent', tier: 1, source: 'local' }, 201),
      },
    })
    await h.flush()
    const rows = h.wrapper.findAll('[data-testid="member-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.find('[data-testid="member-remove"]').exists()).toBe(true)
    expect(rows[1]!.find('[data-testid="member-remove"]').exists()).toBe(false)
    expect(rows[1]!.find('[data-testid="member-idp"]').exists()).toBe(true)
    h.wrapper.findComponent({ name: 'AutocompleteSearch' }).vm.$emit('update:modelValue', 's-9')
    await h.flush()
    await h.wrapper.find('[data-testid="member-add"]').trigger('click')
    await h.flush()
    expect(h.calls('POST /admin/teams/team-1/members')[0]!.body).toEqual({ user_id: 's-9' })
    expect(h.wrapper.emitted('changed')).toBeTruthy()
  })
})
