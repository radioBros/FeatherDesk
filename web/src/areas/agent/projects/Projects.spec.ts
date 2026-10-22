// SPDX-License-Identifier: AGPL-3.0-only

import ProjectsPage from './ProjectsPage.vue'
import ProjectForm from './ProjectForm.vue'
import ProjectMembers from './ProjectMembers.vue'
import ProjectTeams from './ProjectTeams.vue'
import ProjectSla from './ProjectSla.vue'
import ProjectLogo from './ProjectLogo.vue'
import { mountAgent, fx, json, byTestId, typeInto, click } from '../testing'
import { settleConfirm } from '@/composables/useConfirm'
import type { components } from '@/api/schema'

type S = components['schemas']
const ISO = fx.iso
const project = (extra: Partial<S['Project']> = {}): S['Project'] => ({
  ...fx.project(),
  key_locked: false,
  member_count: 2,
  ticket_count: 5,
  ...extra,
})

const web = project({ team_ids: ['team-1'], default_team_id: 'team-1' })
const mob = project({ id: 'p-mob', key: 'MOB', name: 'Mobile', color: '#e91e63', ticket_count: 0, team_ids: [] })

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
  localStorage.clear()
})

describe('ProjectsPage', () => {
  const api = {
    'GET /admin/projects': [web, mob],
    'GET /agent/teams': [fx.team()],
    'GET /agent/projects': [fx.project()],
  }

  it('lists the projects with their keys and offers create to supervisors', async () => {
    const h = await mountAgent(ProjectsPage, { me: fx.me('supervisor'), api })
    expect(h.wrapper.findAll('[data-testid="project-row"]')).toHaveLength(2)
    expect(h.wrapper.text()).toContain('Mobile')
    expect(h.wrapper.find('[data-testid="project-new"]').exists()).toBe(true)
    expect(h.wrapper.findAll('[data-testid="project-delete"]')).toHaveLength(2)
  })

  it('hides create and delete from a project admin', async () => {
    const h = await mountAgent(ProjectsPage, { me: fx.me('agent'), api })
    expect(h.wrapper.find('[data-testid="project-new"]').exists()).toBe(false)
    expect(h.wrapper.find('[data-testid="project-delete"]').exists()).toBe(false)
    expect(h.wrapper.find('[data-testid="project-edit"]').exists()).toBe(true)
  })

  it('has a column menu: the key and name stay locked, the rest toggles', async () => {
    const h = await mountAgent(ProjectsPage, { me: fx.me('supervisor'), api })
    expect(h.wrapper.find('[data-testid="columns-menu"]').exists()).toBe(true)
    expect(h.wrapper.find('th:nth-child(1)').text()).toBe('Key')
    // tickets column is shown by default; sort column is not
    expect(h.wrapper.text()).toContain('Tickets')
    expect(h.wrapper.findAll('thead th').map((t) => t.text())).not.toContain('Order')
  })

  it('shows an empty state', async () => {
    const h = await mountAgent(ProjectsPage, { me: fx.me('supervisor'), api: { ...api, 'GET /admin/projects': [] } })
    expect(h.wrapper.find('[data-testid="empty-state"]').exists()).toBe(true)
  })

  it('creates a project and refreshes the switcher list', async () => {
    const h = await mountAgent(ProjectsPage, {
      me: fx.me('supervisor'),
      api: { ...api, 'POST /admin/projects': (_u, init) => json(project({ id: 'p-new', key: init.body.key, name: init.body.name }), 201) },
    })
    await click(h.wrapper.find('[data-testid="project-new"]').element)
    await typeInto(byTestId('project-key'), 'ops')
    await typeInto(byTestId('project-name'), 'Operations')
    await click(byTestId('project-save'))
    const body = h.calls('POST /admin/projects')[0]!.body as S['ProjectCreate']
    expect(body.key).toBe('OPS')
    expect(body.name).toBe('Operations')
    expect(h.calls('GET /agent/projects').length).toBeGreaterThan(0)
  })

  it('deletes after confirmation', async () => {
    const h = await mountAgent(ProjectsPage, { me: fx.me('supervisor'), api: { ...api, 'DELETE /admin/projects/p-mob': () => new Response(null, { status: 204 }) } })
    await click(h.wrapper.findAll('[data-testid="project-delete"]')[1]!.element)
    settleConfirm(true)
    await h.flush()
    expect(h.calls('DELETE /admin/projects/p-mob')).toHaveLength(1)
  })

  it('opens the detail with tabs and loads members and SLA', async () => {
    const h = await mountAgent(ProjectsPage, {
      me: fx.me('supervisor'),
      api: {
        ...api,
        'GET /admin/projects/p-web/members': [],
        'GET /admin/projects/p-web/sla-policies': [],
        'GET /admin/staff': { items: [], next_cursor: null },
      },
    })
    await click(h.wrapper.find('[data-testid="project-edit"]').element)
    expect(byTestId('project-tabs')).not.toBeNull()
    expect(h.calls('GET /admin/projects/p-web/members')).toHaveLength(1)
    expect(h.calls('GET /admin/projects/p-web/sla-policies')).toHaveLength(1)
  })
})

describe('ProjectForm', () => {
  it('locks the key once the project has tickets and sends only changes', async () => {
    const p = project({ key_locked: true })
    const h = await mountAgent(ProjectForm, {
      props: { project: p, teams: [fx.team()], support: true },
      api: { 'PATCH /admin/projects/p-web': json(p) },
    })
    expect((byTestId('project-key')!.querySelector('input') as HTMLInputElement).disabled).toBe(true)
    await typeInto(byTestId('project-name'), 'Web Platform')
    await click(byTestId('project-save'))
    expect(h.calls('PATCH /admin/projects/p-web')[0]!.body).toEqual({ name: 'Web Platform' })
  })

  it('a project admin never sends key, active or sort', async () => {
    const p = project()
    const h = await mountAgent(ProjectForm, { props: { project: p, teams: [], support: false }, api: { 'PATCH /admin/projects/p-web': json(p) } })
    expect(byTestId('project-active')).toBeNull()
    await typeInto(byTestId('project-portal-title'), 'Hello')
    await click(byTestId('project-save'))
    expect(h.calls('PATCH /admin/projects/p-web')[0]!.body).toEqual({ portal_title: 'Hello' })
  })

  it('rejects an invalid key and an invalid colour without a request', async () => {
    const h = await mountAgent(ProjectForm, { props: { teams: [], support: true } })
    await typeInto(byTestId('project-key'), '9x')
    await typeInto(byTestId('project-name'), 'X')
    await typeInto(byTestId('project-color'), 'nope')
    await click(byTestId('project-save'))
    expect(h.wrapper.text()).toContain('2 to 10 characters')
    expect(h.calls()).toHaveLength(0)
  })

  it('maps a server key error onto the field', async () => {
    const h = await mountAgent(ProjectForm, {
      props: { teams: [], support: true },
      api: { 'POST /admin/projects': () => json({ title: 'Validation', status: 422, errors: [{ field: 'key', code: 'unique' }] }, 422) },
    })
    await typeInto(byTestId('project-key'), 'WEB')
    await typeInto(byTestId('project-name'), 'Dup')
    await click(byTestId('project-save'))
    expect(h.calls('POST /admin/projects')).toHaveLength(1)
    expect(h.wrapper.find('.v-messages__message').exists()).toBe(true)
  })
})

describe('ProjectMembers', () => {
  const m = (id: string, role: S['ProjectRole'] = 'member'): S['ProjectMember'] => ({
    user_id: id, name: `User ${id}`, email: `${id}@x.test`, role, staff_role: 'agent', tier: 1, created_at: ISO,
  })
  it('PUTs the whole list with roles', async () => {
    const h = await mountAgent(ProjectMembers, {
      props: { projectId: 'p-web', members: [m('u1'), m('u2')], staff: [fx.user({ id: 'u3', kind: 'staff', role: 'agent', name: 'Cy', email: 'cy@x.test' })], editable: true },
      api: { 'PUT /admin/projects/p-web/members': json([]) },
    })
    expect(h.wrapper.findAll('[data-testid="project-member-row"]')).toHaveLength(2)
    await click(h.wrapper.findAll('[data-testid="project-member-remove"]')[0]!.element)
    await click(byTestId('project-members-save'))
    expect(h.calls('PUT /admin/projects/p-web/members')[0]!.body).toEqual({ members: [{ user_id: 'u2', role: 'member' }] })
  })
  it('is read-only for project admins', async () => {
    const h = await mountAgent(ProjectMembers, { props: { projectId: 'p-web', members: [m('u1', 'project_admin')], editable: false } })
    expect(h.wrapper.find('[data-testid="project-members-save"]').exists()).toBe(false)
    expect(h.wrapper.find('[data-testid="project-member-remove"]').exists()).toBe(false)
  })
})

describe('ProjectTeams', () => {
  const teams = [fx.team(), fx.team({ id: 'team-2', name: 'Night' })]
  it('refuses to drop the default team', async () => {
    const h = await mountAgent(ProjectTeams, { props: { projectId: 'p-web', teams, teamIds: ['team-1'], defaultTeamId: 'team-1', editable: true } })
    await click(byTestId('project-team-team-1')!.querySelector('input'))
    await click(byTestId('project-teams-save'))
    expect(byTestId('project-teams-error')).not.toBeNull()
    expect(h.calls()).toHaveLength(0)
  })
  it('PUTs the selected teams', async () => {
    const h = await mountAgent(ProjectTeams, {
      props: { projectId: 'p-web', teams, teamIds: ['team-1'], defaultTeamId: 'team-1', editable: true },
      api: { 'PUT /admin/projects/p-web/teams': json({ team_ids: ['team-1', 'team-2'] }) },
    })
    await click(byTestId('project-team-team-2')!.querySelector('input'))
    await click(byTestId('project-teams-save'))
    expect((h.calls('PUT /admin/projects/p-web/teams')[0]!.body as { team_ids: string[] }).team_ids.sort()).toEqual(['team-1', 'team-2'])
  })
})

describe('ProjectSla', () => {
  const policies = (['low', 'normal', 'high', 'urgent'] as const).map((priority) => ({
    priority, first_response_minutes: 60, resolution_minutes: 600, calendar_mode: '24x7' as const, inherited: priority !== 'urgent',
  }))
  it('shows inherited priorities as such and sends only the overrides', async () => {
    const h = await mountAgent(ProjectSla, {
      props: { projectId: 'p-web', policies },
      api: { 'PUT /admin/projects/p-web/sla-policies': json([]) },
    })
    expect(h.wrapper.text()).toContain('Global default')
    await typeInto(byTestId('project-sla-first-urgent'), '15')
    await click(byTestId('project-sla-save'))
    expect(h.calls('PUT /admin/projects/p-web/sla-policies')[0]!.body).toEqual({
      policies: [{ priority: 'urgent', first_response_minutes: 15, resolution_minutes: 600, calendar_mode: '24x7' }],
    })
  })
  it('clearing every override sends an empty list', async () => {
    const h = await mountAgent(ProjectSla, { props: { projectId: 'p-web', policies }, api: { 'PUT /admin/projects/p-web/sla-policies': json([]) } })
    await click(byTestId('project-sla-clear'))
    await click(byTestId('project-sla-save'))
    expect(h.calls('PUT /admin/projects/p-web/sla-policies')[0]!.body).toEqual({ policies: [] })
  })
  it('rejects bad minutes before sending', async () => {
    const h = await mountAgent(ProjectSla, { props: { projectId: 'p-web', policies } })
    await typeInto(byTestId('project-sla-first-urgent'), '0')
    await click(byTestId('project-sla-save'))
    expect(h.calls()).toHaveLength(0)
  })
})

describe('ProjectLogo', () => {
  it('rejects a wrong file type without a request', async () => {
    const h = await mountAgent(ProjectLogo, { props: { project: project() } })
    const input = byTestId('project-logo-input')!.querySelector('input') as HTMLInputElement
    const file = new File(['x'], 'x.exe', { type: 'application/x-msdownload' })
    Object.defineProperty(input, 'files', { value: [file], configurable: true })
    input.dispatchEvent(new Event('change', { bubbles: true }))
    await h.flush()
    expect(h.calls()).toHaveLength(0)
  })
  it('removes the logo', async () => {
    const h = await mountAgent(ProjectLogo, {
      props: { project: project({ logo_url: '/branding/x.png' }) },
      api: { 'DELETE /admin/projects/p-web/logo': () => new Response(null, { status: 204 }) },
    })
    await click(byTestId('project-logo-remove'))
    expect(h.calls('DELETE /admin/projects/p-web/logo')).toHaveLength(1)
    expect(h.wrapper.emitted('changed')![0]![0]).toMatchObject({ logo_url: null })
  })
})
