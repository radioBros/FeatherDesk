// SPDX-License-Identifier: AGPL-3.0-only

import type { ServerEvent } from '@/realtime/client'
import { ref } from 'vue'
import TicketDetailPage from './pages/TicketDetailPage.vue'
import { mockFetch, json, fillMarkdownEditor } from '@/test/mount'
import { settleConfirm } from '@/composables/useConfirm'
import { mountPage, requests, settle } from './testing'
import type { UseRealtimeOptions } from '@/composables/useRealtime'

const rt = vi.hoisted(() => ({ topics: [] as unknown[], opts: null as UseRealtimeOptions | null }))
vi.mock('@/composables/useRealtime', () => ({
  useRealtime: (topics: unknown, opts: UseRealtimeOptions) => {
    rt.topics.push(typeof topics === 'function' ? (topics as () => string[])() : topics)
    rt.opts = opts
    return { connected: ref(true) }
  },
}))
afterEach(() => vi.unstubAllGlobals())

const msg = (id: string, body: string, extra = {}) => ({
  id, ticket_id: 't1', kind: 'public' as const, body_md: body, body_html: `<p>${body}</p>`, attachments: [], created_at: '2026-10-20T10:00:00Z',
  author: { id: 's1', name: 'Agent Smith', kind: 'staff' as const }, ...extra,
})
/** A server event frame, typed by the generated `WsServerFrame` (CON-013). */
const ev = (e: Omit<ServerEvent, 'type' | 'at'>): ServerEvent => ({ type: 'event', at: '2026-10-20T10:00:00Z', ...e })
const ticket = (extra = {}) => ({
  id: 't1', number: 12, subject: 'Printer offline', status: 'open', priority: 'normal',
  requester: { id: 'u-cust', name: 'Carla', kind: 'customer' }, created_at: '2026-10-20T10:00:00Z', updated_at: '2026-10-20T10:00:00Z',
  messages: [msg('m1', 'Hello from support')], allowed_actions: ['reply', 'resolve'], ...extra,
})

function server(tk: () => object, over: Record<string, () => Response> = {}) {
  return mockFetch((url, init) => {
    const path = new URL(url, 'http://x').pathname.replace('/api/v1', '')
    const key = `${(init.method ?? 'GET').toUpperCase()} ${path}`
    if (over[key]) return over[key]!()
    if (key === 'GET /portal/custom-fields') return json([])
    if (key === 'GET /portal/tickets/t1') return json(tk())
    if (key.startsWith('GET /portal/tickets/')) return json({ ...ticket(), id: path.split('/').pop() })
    return json({})
  })
}
const open = () => mountPage(TicketDetailPage, { path: '/tickets/t1', pattern: '/tickets/:id' })

describe('portal ticket detail (FE-017)', () => {
  it('renders the public thread and subscribes to ticket:<id>', async () => {
    server(() => ticket())
    const { wrapper } = await open()
    await settle(8)
    expect(wrapper.find('[data-testid="detail-subject"]').text()).toContain('Printer offline')
    expect(wrapper.find('[data-testid="message-thread"]').text()).toContain('Hello from support')
    expect(rt.topics).toContainEqual(['ticket:t1'])
  })

  it('the reply form passes FE-005: empty submit sends nothing', async () => {
    const { fn } = server(() => ticket())
    const { wrapper } = await open()
    await settle(8)
    const before = fn.mock.calls.length
    await wrapper.find('[data-testid="reply-form"]').trigger('submit')
    await settle()
    expect(fn.mock.calls.length).toBe(before)
    expect(wrapper.text()).toContain('This field is required')
  })

  it('resolve / reopen buttons appear iff present in allowed_actions', async () => {
    server(() => ticket({ allowed_actions: ['reply', 'resolve'] }))
    const a = await open()
    await settle(8)
    expect(a.wrapper.find('[data-testid="resolve-btn"]').exists()).toBe(true)
    expect(a.wrapper.find('[data-testid="reopen-btn"]').exists()).toBe(false)
    a.wrapper.unmount()

    server(() => ticket({ status: 'resolved', allowed_actions: ['reopen'] }))
    const b = await open()
    await settle(8)
    expect(b.wrapper.find('[data-testid="resolve-btn"]').exists()).toBe(false)
    expect(b.wrapper.find('[data-testid="reopen-btn"]').exists()).toBe(true)
    expect(b.wrapper.find('[data-testid="reply-form"]').exists()).toBe(false)
  })

  it('"My issue is solved" posts resolve after confirmation', async () => {
    let state = ticket()
    const { fn } = server(() => state, { 'POST /portal/tickets/t1/resolve': () => json((state = ticket({ status: 'resolved', allowed_actions: ['reopen', 'csat'] }))) })
    const { wrapper } = await open()
    await settle(8)
    await wrapper.find('[data-testid="resolve-btn"]').trigger('click')
    await settle()
    settleConfirm(true)
    await settle(8)
    expect((await requests(fn)).some((r) => r.method === 'POST' && r.path === '/portal/tickets/t1/resolve')).toBe(true)
    expect(wrapper.find('[data-testid="status-chip"]').text()).toBe('Resolved')
    expect(wrapper.find('[data-testid="csat-card"]').exists()).toBe(true)
  })

  it('a reply on a closed ticket navigates to the returned follow-up id', async () => {
    const FOLLOW = '0192f0aa-1111-7222-8333-444455556666'
    server(() => ticket({ status: 'closed', allowed_actions: ['reply'] }), {
      'POST /portal/tickets/t1/messages': () =>
        json({ type: 'x/conflict', title: 'Ticket closed', status: 409, detail: `Reply added to follow-up ticket ${FOLLOW}` }, 409),
    })
    const { wrapper, router } = await open()
    await settle(8)
    expect(wrapper.find('[data-testid="closed-notice"]').text()).toContain('Replying will open a follow-up ticket')
    await fillMarkdownEditor(wrapper, 'It broke again')
    await wrapper.find('[data-testid="reply-form"]').trigger('submit')
    await settle(8)
    expect(router.currentRoute.value.path).toBe(`/tickets/${FOLLOW}`)
  })

  it('a sent reply is appended and carries an Idempotency-Key', async () => {
    const { fn } = server(() => ticket(), { 'POST /portal/tickets/t1/messages': () => json(msg('m2', 'Thanks!', { author: { id: 'u-cust', name: 'Carla', kind: 'customer' } }), 201) })
    const { wrapper } = await open()
    await settle(8)
    await fillMarkdownEditor(wrapper, 'Thanks!')
    await wrapper.find('[data-testid="reply-form"]').trigger('submit')
    await settle(8)
    const post = (await requests(fn)).find((r) => r.method === 'POST')!
    expect(post.body).toMatchObject({ body_md: 'Thanks!', attachment_ids: [] })
    expect(post.headers.get('Idempotency-Key')).toBeTruthy()
    expect(wrapper.findAll('[data-testid="msg-public"]')).toHaveLength(2)
  })

  it('a message.created frame appends to the thread with no HTTP request; internal kinds are ignored', async () => {
    const { fn } = server(() => ticket())
    const { wrapper } = await open()
    await settle(8)
    const before = fn.mock.calls.length
    rt.opts!.onEvent!(ev({ topic: 'ticket:t1', event_id: 'e1', event: 'message.created', data: { message: msg('m9', 'Live reply') } }))
    // Deliberately off-spec (the portal API never sends it, I-2): the view must still drop it.
    rt.opts!.onEvent!(ev({ topic: 'ticket:t1', event_id: 'e2', event: 'message.created', data: { message: msg('m10', 'SECRET NOTE', { kind: 'internal' }) as never } }))
    await settle()
    expect(fn.mock.calls.length).toBe(before)
    expect(wrapper.text()).toContain('Live reply')
    expect(wrapper.text()).not.toContain('SECRET NOTE')
  })

  it('ticket.access_lost navigates back to the list', async () => {
    server(() => ticket())
    const { router } = await open()
    await settle(8)
    rt.opts!.onEvent!(ev({ topic: 'ticket:t1', event_id: 'e3', event: 'ticket.access_lost', data: {} }))
    await settle(4)
    expect(router.currentRoute.value.path).toBe('/tickets')
  })

  it('a status event refetches the ticket once; resync refetches too', async () => {
    let state = ticket()
    const { fn } = server(() => state)
    const { wrapper } = await open()
    await settle(8)
    const gets = async () => (await requests(fn)).filter((r) => r.path === '/portal/tickets/t1').length
    expect(await gets()).toBe(1)
    state = ticket({ status: 'awaiting_reply' })
    rt.opts!.onEvent!(ev({ topic: 'ticket:t1', event_id: 'e4', event: 'ticket.status_changed', data: {} }))
    await settle(8)
    expect(await gets()).toBe(2)
    expect(wrapper.find('[data-testid="status-chip"]').text()).toBe('Awaiting your reply')
    rt.opts!.onResync!('gap_too_large')
    await settle(8)
    expect(await gets()).toBe(3)
  })

  it('a 404 shows the not-found state', async () => {
    server(() => ticket(), { 'GET /portal/tickets/t1': () => json({ type: 'x/not-found', title: 'Not found', status: 404 }, 404) })
    const { wrapper } = await open()
    await settle(8)
    expect(wrapper.find('[data-testid="detail-error"]').exists()).toBe(true)
  })
})
