// SPDX-License-Identifier: AGPL-3.0-only

import TicketListPage from '../TicketListPage.vue'
import { mountAgent, fx, json } from '../../testing'
import type { AgentHarness, MountAgentOptions } from '../../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
  try {
    localStorage.clear()
  } catch {
    /* ignore */
  }
})

const PLAN = {
  id: 'cf-1',
  key: 'plan',
  type: 'select',
  label: { en: 'Plan' },
  options: [
    { value: 'gold', label: { en: 'Gold' } },
    { value: 'free', label: { en: 'Free' } },
  ],
  required: false,
  customer_visible: false,
  customer_editable: false,
  active: true,
  sort: 1,
}

function base(items = [fx.summary(), fx.summary({ id: 't-2', number: 1043, subject: 'Mouse missing', status: 'closed' })], extra = {}) {
  return {
    'GET /agent/tickets': () => json(fx.page(items)),
    'GET /agent/teams': [fx.team()],
    'GET /agent/categories': [fx.category()],
    'GET /agent/staff': [fx.staffRef()],
    'GET /agent/custom-fields': [PLAN],
    'GET /agent/views': [],
    ...extra,
  }
}
const mountList = (route: string, api: Record<string, unknown> = base()) =>
  mountAgent(TicketListPage, { path: '/agent/tickets', route, me: fx.me('agent', 2), api: api as MountAgentOptions<typeof TicketListPage>['api'] })
const ticketCalls = (h: AgentHarness) => h.calls('GET /agent/tickets')
const rows = (h: AgentHarness) => h.wrapper.findAll('[data-testid="ticket-row"]')

describe('TicketListPage (FE-021)', () => {
  it('URL ?status=open&tier=2 yields that API query, with the default attention sort', async () => {
    const h = await mountList('/agent/tickets?status=open&tier=2')
    const p = ticketCalls(h)[0]!.url.searchParams
    expect(p.get('status')).toBe('open')
    expect(p.get('tier')).toBe('2')
    expect(p.get('sort')).toBe('attention')
  })

  it('queue views map to API filters', async () => {
    const mine = await mountList('/agent/tickets?view=mine')
    expect(ticketCalls(mine)[0]!.url.searchParams.get('assignee')).toBe('me')
    mine.wrapper.unmount()
    const triage = await mountList('/agent/tickets?view=triage')
    expect(ticketCalls(triage)[0]!.url.searchParams.get('team_id')).toBe('none')
  })

  it('passes custom-field filters through as cf.<key>', async () => {
    const h = await mountList('/agent/tickets?cf.plan=gold')
    expect(ticketCalls(h)[0]!.url.searchParams.get('cf.plan')).toBe('gold')
    // the filter widget shows the restored value
    expect(h.wrapper.find('[data-testid="cf-filters"]').text()).toContain('Gold')
  })

  it('restores filters from the URL (reload) and writes changes back to it', async () => {
    const h = await mountList('/agent/tickets?status=open,pending&sort=-updated_at')
    expect(h.wrapper.find('[data-testid="filter-status"]').text()).toContain('Open')
    expect(h.wrapper.find('[data-testid="filter-status"]').text()).toContain('Pending')
    expect(ticketCalls(h)[0]!.url.searchParams.get('status')).toBe('open,pending')
    const q = h.wrapper.find('[data-testid="filter-q"] input')
    await q.setValue('printer')
    await q.trigger('keydown', { key: 'Enter' })
    await h.flush()
    expect(h.router.currentRoute.value.query).toMatchObject({ status: 'open,pending', sort: '-updated_at', q: 'printer' })
    expect(ticketCalls(h).at(-1)!.url.searchParams.get('q')).toBe('printer')
  })

  it('renders exactly what the server returned (no client-side filtering)', async () => {
    const h = await mountList('/agent/tickets?status=open')
    expect(rows(h)).toHaveLength(2)
    expect(h.wrapper.text()).toContain('Mouse missing') // a closed ticket the server chose to return
  })

  it('loads the next page with the cursor', async () => {
    let n = 0
    const h = await mountList('/agent/tickets', {
      ...base(),
      'GET /agent/tickets': () => json(n++ === 0 ? fx.page([fx.summary()], 'c2') : fx.page([fx.summary({ id: 't-9', number: 9 })])),
    })
    await h.wrapper.find('[data-testid="load-more"]').trigger('click')
    await h.flush()
    expect(ticketCalls(h)[1]!.url.searchParams.get('cursor')).toBe('c2')
    expect(rows(h)).toHaveLength(2)
  })

  it('a tickets frame patches a known row without any HTTP request', async () => {
    const h = await mountList('/agent/tickets')
    expect(h.socket().frames('subscribe').flatMap((f) => f.topics as string[])).toContain('tickets')
    const before = h.calls().length
    h.socket().push({
      type: 'event',
      at: '2026-10-20T10:00:00Z',
      topic: 'tickets',
      event_id: 'e-1',
      event: 'ticket.status_changed',
      data: { ticket: fx.summary({ subject: 'Printer extinguished', status: 'pending' }) },
    })
    await h.flush()
    expect(rows(h)[0]!.text()).toContain('Printer extinguished')
    expect(rows(h)[0]!.text()).toContain('Pending')
    expect(h.calls().length).toBe(before)
  })

  it('an unknown ticket triggers one debounced refetch', async () => {
    const h = await mountList('/agent/tickets')
    for (const id of ['t-50', 't-51'])
      h.socket().push({ type: 'event', at: '2026-10-20T10:00:00Z', topic: 'tickets', event_id: id, event: 'ticket.created', data: { ticket: fx.summary({ id, number: 50 }) } })
    await new Promise((r) => setTimeout(r, 1100))
    await h.flush()
    expect(ticketCalls(h)).toHaveLength(2)
  })

  it('opens a saved view from ?view_id= by applying its filter', async () => {
    const h = await mountList('/agent/tickets?view_id=v-1', {
      ...base(),
      'GET /agent/views/v-1': { id: 'v-1', name: 'Open gold', shared: false, filter: { status: 'open', 'cf.plan': 'gold' } },
    })
    await h.flush()
    expect(h.router.currentRoute.value.query).toMatchObject({ status: 'open', 'cf.plan': 'gold', view_id: 'v-1' })
    const p = ticketCalls(h).at(-1)!.url.searchParams
    expect(p.get('status')).toBe('open')
    expect(p.get('cf.plan')).toBe('gold')
    expect(p.get('view_id')).toBeNull()
  })

  it('custom-field columns are chosen per user and persisted', async () => {
    localStorage.setItem('st.v1.agent.columns', JSON.stringify(['plan']))
    const h = await mountList('/agent/tickets', base([fx.summary({ custom_fields: { plan: 'gold' } })]))
    expect(h.wrapper.find('[data-testid="cfv-plan"]').text()).toBe('Gold')
  })

  it('row click opens the ticket by number', async () => {
    const h = await mountList('/agent/tickets')
    await rows(h)[1]!.trigger('click')
    await h.flush()
    expect(h.router.currentRoute.value.path).toBe('/agent/tickets/1043')
  })
})
