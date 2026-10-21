// SPDX-License-Identifier: AGPL-3.0-only

import TicketListPage from './TicketListPage.vue'
import TicketDetailPage from './TicketDetailPage.vue'
import TicketNewPage from './TicketNewPage.vue'
import { mountAgent, fx, json, byTestId, click, type AgentHarness } from '../testing'
import { useProjectsStore } from '@/stores/projects'
import { eventDetail, eventLabel } from './detail/events'

afterEach(() => {
  document.body.innerHTML = ''
  try {
    localStorage.clear()
  } catch {
    /* ignore */
  }
})

const WEB = fx.project()
const MOB = fx.project({ id: 'p-mob', key: 'MOB', name: 'Mobile', color: '#d81b60' })
const mobTicket = fx.summary({ id: 't-7', number: 7, ref: 'MOB-7', project_id: 'p-mob', project: { id: 'p-mob', key: 'MOB', name: 'Mobile', color: '#d81b60' } })
const lookups = {
  'GET /agent/projects': [WEB, MOB],
  'GET /agent/teams': [fx.team()],
  'GET /agent/categories': [fx.category()],
  'GET /agent/staff': [fx.staffRef()],
  'GET /agent/custom-fields': [],
  'GET /agent/tags': [],
  'GET /agent/views': [],
  'PATCH /me': {},
}

const mountList = async (route = '/agent/tickets', me = {}) => {
  const h = await mountAgent(TicketListPage, {
    path: '/agent/tickets',
    route,
    me: fx.me('agent', 2, me),
    api: { ...lookups, 'GET /agent/tickets': () => json(fx.page([fx.summary(), mobTicket])) } as never,
  })
  return h
}
const ticketCalls = (h: AgentHarness) => h.calls('GET /agent/tickets')

describe('ticket list and projects', () => {
  it('sends the switcher project as the project filter', async () => {
    const h = await mountAgent(TicketListPage, {
      path: '/agent/tickets',
      route: '/agent/tickets',
      me: fx.me('agent', 2, { current_project_id: 'p-mob' }),
      api: { ...lookups, 'GET /agent/tickets': () => json(fx.page([mobTicket])) } as never,
    })
    expect(ticketCalls(h)).toHaveLength(1)
    expect(ticketCalls(h)[0]!.url.searchParams.get('project')).toBe('p-mob')
    await useProjectsStore().select('p-web')
    await h.flush()
    const calls = ticketCalls(h)
    expect(calls.at(-1)!.url.searchParams.get('project')).toBe('p-web')
  })

  it('an explicit ?project= wins over the switcher and no filter is sent for all projects', async () => {
    const h = await mountList('/agent/tickets?project=MOB')
    expect(ticketCalls(h)[0]!.url.searchParams.get('project')).toBe('MOB')
    const all = await mountList()
    expect(ticketCalls(all)[0]!.url.searchParams.has('project')).toBe(false)
  })

  it('shows refs and project chips; the ref opens the ticket', async () => {
    const h = await mountList()
    const rows = h.wrapper.findAll('[data-testid="ticket-row"]')
    expect(rows[1]!.text()).toContain('MOB-7')
    expect(rows[1]!.find('[data-testid="project-chip"]').text()).toBe('MOB')
    await rows[1]!.trigger('click')
    await h.flush()
    expect(h.router.currentRoute.value.path).toBe('/agent/tickets/MOB-7')
  })

  it('the ref column is locked; the project column can be switched off', async () => {
    const h = await mountList()
    await click(byTestId('columns-menu'))
    const ref = byTestId('column-toggle-number')!.querySelector('input')!
    expect(ref.disabled).toBe(true)
    const proj = byTestId('column-toggle-project')!.querySelector('input')!
    expect(proj.disabled).toBe(false)
    expect(h.wrapper.find('[data-testid="project-chip"]').exists()).toBe(true)
    await click(proj)
    await h.flush()
    expect(h.wrapper.find('[data-testid="project-chip"]').exists()).toBe(false)
  })
})

describe('ticket detail by ref and move', () => {
  const open = (ticket = fx.ticket(), api: Record<string, unknown> = {}, me = fx.me('agent', 1)) =>
    mountAgent(TicketDetailPage, {
      path: '/agent/tickets/:ref([A-Za-z][A-Za-z0-9]*-\\d+)',
      route: `/agent/tickets/${ticket.ref}`,
      me,
      api: { ...lookups, [`GET /agent/tickets/by-ref/${ticket.ref}`]: ticket, ...api } as never,
    })

  it('loads by ref and shows ref and project chip in the header', async () => {
    const h = await open()
    expect(h.calls('GET /agent/tickets/by-ref/WEB-1042')).toHaveLength(1)
    const head = byTestId('ticket-header')!
    expect(head.textContent).toContain('WEB-1042')
    expect(head.querySelector('[data-testid="project-chip"]')?.textContent).toContain('WEB')
  })

  it('moves the ticket with PATCH project_id and continues on the new ref', async () => {
    const moved = fx.ticket({ ref: 'MOB-3', project_id: 'p-mob', project: { id: 'p-mob', key: 'MOB', name: 'Mobile', color: '' } })
    const h = await open(fx.ticket(), { 'PATCH /agent/tickets/t-1': () => json({ ticket: moved, visible: true }), 'GET /agent/tickets/t-1': moved })
    await useProjectsStore().load()
    await h.flush()
    await click(byTestId('ticket-more'))
    await click(byTestId('move-open'))
    await h.flush()
    // Submitting empty shows the required error and sends nothing.
    await click(byTestId('move-submit'))
    expect(h.calls('PATCH /agent/tickets/t-1')).toHaveLength(0)
    const form = h.wrapper.findComponent({ name: 'MoveProjectForm' })
    ;(form.vm as unknown as { $: { setupState: { model: { project_id: string } } } }).$.setupState.model.project_id = 'p-mob'
    await h.flush()
    await click(byTestId('move-submit'))
    await h.flush()
    expect(h.calls('PATCH /agent/tickets/t-1')[0]!.body).toEqual({ project_id: 'p-mob' })
    expect(h.router.currentRoute.value.path).toBe('/agent/tickets/MOB-3')
  })

  it('offers the move only with the transfer action and another active project', async () => {
    const noTransfer = await open(fx.ticket({ allowed_actions: ['reply', 'merge'] }))
    await useProjectsStore().load()
    await noTransfer.flush()
    expect(byTestId('move-open')).toBeNull()
    document.body.innerHTML = ''
    const single = await open(fx.ticket({ allowed_actions: ['transfer', 'merge'] }), { 'GET /agent/projects': [WEB] })
    await useProjectsStore().load(true)
    await single.flush()
    await click(byTestId('ticket-more'))
    expect(byTestId('move-open')).toBeNull()
  })

  it('a 422 not_allowed from the server shows on the target field', async () => {
    const h = await open(fx.ticket(), {
      'PATCH /agent/tickets/t-1': () => json({ title: 'x', status: 422, errors: [{ field: 'project_id', code: 'not_allowed' }] }, 422),
    })
    await useProjectsStore().load()
    await h.flush()
    await click(byTestId('ticket-more'))
    await click(byTestId('move-open'))
    const form = h.wrapper.findComponent({ name: 'MoveProjectForm' })
    ;(form.vm as unknown as { $: { setupState: { model: { project_id: string } } } }).$.setupState.model.project_id = 'p-mob'
    await h.flush()
    await click(byTestId('move-submit'))
    await h.flush()
    expect(byTestId('move-project-form')!.textContent).toContain('not allowed')
  })

  it('the legacy numeric link still loads by number', async () => {
    const h = await mountAgent(TicketDetailPage, {
      path: '/agent/tickets/:number(\\d+)',
      route: '/agent/tickets/1042',
      me: fx.me('agent', 1),
      api: { ...lookups, 'GET /agent/tickets/by-number/1042': fx.ticket() } as never,
    })
    expect(h.calls('GET /agent/tickets/by-number/1042')).toHaveLength(1)
  })

  it('timeline labels the project move', () => {
    expect(eventLabel('ticket.project_changed')).toBe('Moved to another project')
    expect(eventDetail({ type: 'ticket.project_changed', data: { from_ref: 'WEB-1', to_ref: 'MOB-3' } })).toBe('WEB-1 → MOB-3')
  })
})

describe('new ticket and projects', () => {
  const mountNew = (projects: unknown[], me = {}) =>
    mountAgent(TicketNewPage, {
      path: '/agent/tickets/new',
      me: fx.me('agent', 1, me),
      api: { ...lookups, 'GET /agent/projects': projects, 'POST /agent/tickets': () => json(fx.ticket({ ref: 'MOB-1', number: 9 })) } as never,
    })

  it('shows the project select with several projects, preselected from the switcher', async () => {
    const h = await mountNew([WEB, MOB], { current_project_id: 'p-mob' })
    await h.flush()
    expect(byTestId('ticket-project')).not.toBeNull()
    expect(byTestId('ticket-project')!.textContent).toContain('Mobile')
  })

  it('hides it with a single project', async () => {
    const h = await mountNew([WEB])
    await h.flush()
    expect(byTestId('ticket-project')).toBeNull()
  })
})
