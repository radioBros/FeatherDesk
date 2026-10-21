// SPDX-License-Identifier: AGPL-3.0-only

import type { ServerEvent } from '@/realtime/client'
import { ref } from 'vue'
import TicketListPage from './pages/TicketListPage.vue'
import { mockFetch, json } from '@/test/mount'
import { DEFAULT_BRANDING } from '@/stores/config'
import { mountPage, requests, settle, ORG_MANAGER } from './testing'
import type { UseRealtimeOptions } from '@/composables/useRealtime'

const rt = vi.hoisted(() => ({ topics: [] as unknown[], opts: null as UseRealtimeOptions | null }))
vi.mock('@/composables/useRealtime', () => ({
  useRealtime: (topics: unknown, opts: UseRealtimeOptions) => {
    rt.topics.push(topics)
    rt.opts = opts
    return { connected: ref(true) }
  },
}))

afterEach(() => vi.unstubAllGlobals())

const WEB = { id: 'p-web', key: 'WEB', name: 'Web App', color: '#1976d2' }
const MOB = { id: 'p-mob', key: 'MOB', name: 'Mobile', color: '#d81b60' }
const row = (id: string, extra = {}) => ({
  id,
  number: Number(id.replace(/\D/g, '')) || 1,
  subject: `Subject ${id}`,
  status: 'open' as const,
  priority: 'normal' as const,
  requester: { id: 'u-cust', name: 'Carla', kind: 'customer' as const },
  ref: `WEB-${Number(id.replace(/\D/g, '')) || 1}`,
  project_id: 'p-web',
  project: WEB,
  created_at: '2026-10-20T10:00:00Z',
  updated_at: '2026-10-20T10:00:00Z',
  ...extra,
})

const ticketRequests = async (fn: Parameters<typeof requests>[0]) => (await requests(fn)).filter((r) => r.path === '/portal/tickets')

/** A server event frame, typed by the generated `WsServerFrame` (CON-013). */
const ev = (e: Omit<ServerEvent, 'type' | 'at'>): ServerEvent => ({ type: 'event', at: '2026-10-20T10:00:00Z', ...e })

describe('portal ticket list (FE-015)', () => {
  it('requests /portal/tickets with the selected filters', async () => {
    const { fn } = mockFetch(() => json({ items: [row('t1')], next_cursor: null }))
    const { wrapper } = await mountPage(TicketListPage, { path: '/tickets?status=resolved&q=printer', pattern: '/tickets' })
    await settle(6)
    const [first] = await ticketRequests(fn)
    expect(first!.path).toBe('/portal/tickets')
    const params = new URLSearchParams(first!.search)
    expect(params.get('status')).toBe('resolved')
    expect(params.get('q')).toBe('printer')
    expect(params.has('mine_only')).toBe(false)
    expect(wrapper.findAll('[data-testid="ticket-row"]')).toHaveLength(1)
    expect(rt.topics).toContainEqual(['tickets'])
  })

  it('renders the Organization toggle only for org managers and sends mine_only', async () => {
    mockFetch(() => json({ items: [], next_cursor: null }))
    const requester = await mountPage(TicketListPage, { path: '/tickets' })
    await settle()
    expect(requester.wrapper.find('[data-testid="scope-toggle"]').exists()).toBe(false)
    requester.wrapper.unmount()

    const { fn } = mockFetch(() => json({ items: [], next_cursor: null }))
    const mgr = await mountPage(TicketListPage, { path: '/tickets', me: ORG_MANAGER })
    await settle()
    expect(mgr.wrapper.find('[data-testid="scope-toggle"]').exists()).toBe(true)
    const buttons = mgr.wrapper.findAll('[data-testid="scope-toggle"] button')
    await buttons[0]!.trigger('click')
    await settle(6)
    const last = (await ticketRequests(fn)).at(-1)!
    expect(new URLSearchParams(last.search).get('mine_only')).toBe('true')
  })

  it('an empty list shows the New ticket CTA and the configured welcome text', async () => {
    mockFetch(() => json({ items: [], next_cursor: null }))
    const { wrapper } = await mountPage(TicketListPage, {
      path: '/tickets',
      config: { branding: { ...DEFAULT_BRANDING, portal_welcome_md: '**Welcome to ACME support**' } },
    })
    await settle(6)
    expect(wrapper.find('[data-testid="empty-new-ticket"]').attributes('href')).toBe('/tickets/new')
    expect(wrapper.find('[data-testid="portal-welcome"]').html()).toContain('<strong>Welcome to ACME support</strong>')
  })

  it('loads more with the cursor', async () => {
    const { fn } = mockFetch((url) =>
      json(url.includes('cursor=c2') ? { items: [row('t2')], next_cursor: null } : { items: [row('t1')], next_cursor: 'c2' }),
    )
    const { wrapper } = await mountPage(TicketListPage, { path: '/tickets' })
    await settle(6)
    await wrapper.find('[data-testid="load-more"]').trigger('click')
    await settle(6)
    expect(wrapper.findAll('[data-testid="ticket-row"]')).toHaveLength(2)
    expect(new URLSearchParams((await ticketRequests(fn))[1]!.search).get('cursor')).toBe('c2')
    expect(wrapper.find('[data-testid="load-more"]').exists()).toBe(false)
  })

  it('patches rows from realtime frames without an HTTP request, and refetches on resync', async () => {
    const { fn } = mockFetch(() => json({ items: [row('t1')], next_cursor: null }))
    const { wrapper } = await mountPage(TicketListPage, { path: '/tickets' })
    await settle(6)
    const before = fn.mock.calls.length
    rt.opts!.onEvent!(ev({ topic: 'tickets', event_id: 'e1', event: 'ticket.status_changed', data: { ticket: row('t1', { status: 'resolved', subject: 'Fixed subject' }) } }))
    rt.opts!.onEvent!(ev({ topic: 'tickets', event_id: 'e2', event: 'ticket.created', data: { ticket: row('t9') } }))
    await settle()
    expect(fn.mock.calls.length).toBe(before)
    expect(wrapper.text()).toContain('Fixed subject')
    expect(wrapper.findAll('[data-testid="ticket-row"]')).toHaveLength(2)
    rt.opts!.onResync!('gap_too_large')
    await settle(6)
    expect(fn.mock.calls.length).toBe(before + 1)
  })

  it('shows the ref, and a project column and filter only with several projects', async () => {
    mockFetch((url) =>
      new URL(url, 'http://x').pathname.endsWith('/portal/projects')
        ? json([{ ...WEB, description: '', icon: '', logo_url: null, portal_intro_md: null, portal_title: null, primary_color: null }, { ...MOB, description: '', icon: '', logo_url: null, portal_intro_md: null, portal_title: null, primary_color: null }])
        : json({ items: [row('t1'), row('t2', { project_id: 'p-mob', project: MOB, ref: 'MOB-7' })], next_cursor: null }),
    )
    const { wrapper } = await mountPage(TicketListPage, { path: '/tickets' })
    await settle(8)
    const rows = wrapper.findAll('[data-testid="ticket-row"]')
    expect(rows[0]!.text()).toContain('WEB-1')
    expect(rows[1]!.text()).toContain('MOB-7')
    expect(wrapper.findAll('[data-testid="project-chip"]').length).toBe(2)
    expect(wrapper.find('[data-testid="project-filter"]').exists()).toBe(true)
  })

  it('hides the project column and filter when there is a single project', async () => {
    mockFetch((url) => (new URL(url, 'http://x').pathname.endsWith('/portal/projects') ? json([{ ...WEB }]) : json({ items: [row('t1')], next_cursor: null })))
    const { wrapper } = await mountPage(TicketListPage, { path: '/tickets' })
    await settle(8)
    expect(wrapper.find('[data-testid="project-chip"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="project-filter"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="ticket-row"]').text()).toContain('WEB-1')
  })
})
