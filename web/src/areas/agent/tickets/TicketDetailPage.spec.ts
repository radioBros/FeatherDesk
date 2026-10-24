// SPDX-License-Identifier: AGPL-3.0-only

import TicketDetailPage from './TicketDetailPage.vue'
import ComposerForm from './detail/ComposerForm.vue'
import PropertiesPanel from './detail/PropertiesPanel.vue'
import { mountAgent, fx, json, byTestId, allByTestId, typeInto, click, type AgentHarness } from '../testing'
import { useUiStore } from '@/stores/ui'
import type { components } from '@/api/schema'

type S = components['schemas']

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
  document.body.innerHTML = ''
})

const lookups = {
  'GET /agent/teams': [fx.team(), fx.team({ id: 'team-2', name: 'Escalations' })],
  'GET /agent/categories': [fx.category()],
  'GET /agent/custom-fields': [],
  'GET /agent/tags': [],
  'GET /agent/staff': [fx.staffRef()],
}

async function open(ticket: S['Ticket'] = fx.ticket(), api: Record<string, unknown> = {}, me = fx.me('agent', 1)): Promise<AgentHarness> {
  return mountAgent(TicketDetailPage, {
    path: '/agent/tickets/:number',
    route: `/agent/tickets/${ticket.number}`,
    me,
    api: { ...lookups, [`GET /agent/tickets/by-number/${ticket.number}`]: ticket, ...api } as never,
  })
}

const composer = (h: AgentHarness) => h.wrapper.findComponent(ComposerForm)
const panel = (h: AgentHarness) => h.wrapper.findComponent(PropertiesPanel)

describe('TicketDetailPage — thread and composer (FE-022, FE-045)', () => {
  it('renders the subject and the agent thread', async () => {
    const h = await open()
    expect(byTestId('ticket-subject')?.textContent).toContain('Printer on fire')
    expect(h.wrapper.findAll('[data-testid="msg-public"]')).toHaveLength(1)
  })

  it('an empty send makes no request', async () => {
    const h = await open()
    await h.wrapper.find('[data-testid="composer"]').trigger('submit')
    await h.flush()
    expect(h.calls(/^POST/)).toHaveLength(0)
    expect(h.wrapper.text()).toContain('This field is required')
  })

  it('a note posts kind: internal with an Idempotency-Key and appends once', async () => {
    const msg = { id: 'm-2', ticket_id: 't-1', kind: 'internal' as const, body_md: 'Checked the logs', body_html: '<p>Checked the logs</p>', created_at: fx.iso, attachments: [], author: { id: 'u-me', name: 'Alice Agent', kind: 'staff' as const } }
    const h = await open(fx.ticket(), { 'POST /agent/tickets/t-1/messages': () => json(msg, 201) })
    await click(byTestId('mode-internal'))
    await typeInto(byTestId('composer-body'), 'Checked the logs')
    await h.wrapper.find('[data-testid="composer"]').trigger('submit')
    await h.flush()
    const post = h.calls('POST /agent/tickets/t-1/messages')
    expect(post).toHaveLength(1)
    expect(post[0]!.body).toMatchObject({ kind: 'internal', body_md: 'Checked the logs' })
    expect(post[0]!.headers.get('Idempotency-Key')).toBeTruthy()
    // the same message also arriving over WS renders once
    h.socket().push({ type: 'event', at: '2026-10-20T10:00:00Z', topic: 'ticket:t-1', event: 'note.created', event_id: 'e1', data: { message: msg } })
    await h.flush()
    expect(h.wrapper.findAll('[data-testid="msg-internal"]')).toHaveLength(1)
  })

  it('"Send and set pending" posts set_status: pending', async () => {
    const h = await open(fx.ticket(), {
      'POST /agent/tickets/t-1/messages': () => json({ id: 'm-3', ticket_id: 't-1', kind: 'public', body_md: 'x', created_at: fx.iso, attachments: [] }, 201),
      'GET /agent/tickets/t-1': () => json(fx.ticket({ status: 'pending' })),
    })
    await typeInto(byTestId('composer-body'), 'Please send the logs')
    await (composer(h).vm as unknown as { send: (s: string) => Promise<boolean> }).send('pending')
    await h.flush()
    expect(h.calls('POST /agent/tickets/t-1/messages')[0]!.body).toMatchObject({ kind: 'public', set_status: 'pending' })
  })

  it('a retry after a failure re-sends the same Idempotency-Key and renders one message', async () => {
    let n = 0
    const msg = { id: 'm-9', ticket_id: 't-1', kind: 'public', body_md: 'Hello', created_at: fx.iso, attachments: [] }
    const h = await open(fx.ticket(), {
      'POST /agent/tickets/t-1/messages': () => (++n === 1 ? json({ title: 'Server error', status: 500 }, 500) : json(msg, 201)),
    })
    await typeInto(byTestId('composer-body'), 'Hello')
    const form = h.wrapper.find('[data-testid="composer"]')
    await form.trigger('submit')
    await h.flush()
    await form.trigger('submit')
    await h.flush()
    const posts = h.calls('POST /agent/tickets/t-1/messages')
    expect(posts).toHaveLength(2)
    expect(posts[0]!.headers.get('Idempotency-Key')).toBe(posts[1]!.headers.get('Idempotency-Key'))
    expect(h.wrapper.findAll('[data-testid="msg-public"]').filter((w) => w.text().includes('Hello'))).toHaveLength(1)
  })

  it('a 409 invalid-transition on send shows the allowed statuses and re-sends with the pick', async () => {
    const posts: unknown[] = []
    const h = await open(fx.ticket(), {
      'POST /agent/tickets/t-1/messages': (_u: URL, init: { body?: unknown }) => {
        posts.push(init.body)
        return posts.length === 1
          ? json({ type: 'https://featherdesk.dev/problems/invalid-transition', title: 'Invalid transition', status: 409, allowed: ['open', 'resolved'] }, 409)
          : json({ id: 'm-4', ticket_id: 't-1', kind: 'public', body_md: 'x', created_at: fx.iso, attachments: [] }, 201)
      },
      'GET /agent/tickets/t-1': fx.ticket(),
    })
    await typeInto(byTestId('composer-body'), 'Done')
    await (composer(h).vm as unknown as { send: (s: string) => Promise<boolean> }).send('pending')
    await h.flush()
    expect(byTestId('transition-dialog')).toBeTruthy()
    expect(byTestId('transition-resolved')).toBeTruthy()
    expect(byTestId('transition-pending')).toBeNull()
    await click(byTestId('transition-resolved'))
    await h.flush()
    expect(posts[1]).toMatchObject({ set_status: 'resolved', body_md: 'Done' })
  })
})

describe('TicketDetailPage — properties (FE-023, FE-045)', () => {
  it('controls follow allowed_actions only', async () => {
    const h = await open(fx.ticket({ allowed_actions: ['reply', 'note'] }))
    expect(byTestId('prop-status')?.querySelector('.v-input--disabled, .v-field--disabled') ?? byTestId('prop-status')?.className).toBeTruthy()
    expect(byTestId('prop-assignee')?.className).toContain('disabled')
    expect(byTestId('assign-me')).toBeNull()
    expect(byTestId('escalate-open')).toBeNull()
    expect(byTestId('ticket-more')).toBeNull()
    expect(h.wrapper.find('[data-testid="mode-public"]').exists()).toBe(true)
  })

  it('an L3 agent fixture without `escalate` sees no Escalate button; with `deescalate` sees De-escalate', async () => {
    await open(fx.ticket({ tier: 3, allowed_actions: ['reply', 'deescalate'] }), {}, fx.me('agent', 3))
    expect(byTestId('escalate-open')).toBeNull()
    expect(byTestId('deescalate-open')).toBeTruthy()
  })

  it('the assignee picker lists only the users from /agent/assignable', async () => {
    const h = await open(fx.ticket(), { 'GET /agent/assignable': [fx.staffRef({ id: 'u-7', name: 'Carol Close' })] })
    const input = byTestId('prop-assignee')!.querySelector('input')!
    input.dispatchEvent(new Event('focus'))
    await h.flush()
    const call = h.calls('GET /agent/assignable')
    expect(call).toHaveLength(1)
    expect(call[0]!.url.searchParams.get('ticket_id')).toBe('t-1')
    const items = (panel(h).vm as unknown as { $: { setupState: { assigneeItems: { title: string }[] } } }).$.setupState.assigneeItems
    expect(items.map((i) => i.title)).toEqual(['Carol Close'])
  })

  it('the first PATCH after opening carries If-Match (ETag fetched by id), and so does the next one', async () => {
    let version = 1
    const h = await open(fx.ticket(), {
      'GET /agent/tickets/t-1': () => json(fx.ticket(), 200, { ETag: `W/"${version}"` }),
      'PATCH /agent/tickets/t-1': () => {
        version++
        return json({ ticket: fx.ticket({ assignee: { id: 'u-me', name: 'Alice Agent', kind: 'staff' } }), visible: true })
      },
    })
    await h.flush()
    await click(byTestId('assign-me'))
    await h.flush()
    panel(h).vm.$emit('patch', { priority: 'high' })
    await h.flush()
    const patches = h.calls('PATCH /agent/tickets/t-1')
    expect(patches[0]!.body).toEqual({ assignee_id: 'u-me' })
    expect(patches[0]!.headers.get('If-Match')).toBe('W/"1"')
    // The PATCH response has no ETag, so it is refreshed by id before the next change.
    expect(patches[1]!.headers.get('If-Match')).toBe('W/"2"')
  })

  it('without an ETag the PATCH omits If-Match (last-write-wins)', async () => {
    const h = await open(fx.ticket(), { 'PATCH /agent/tickets/t-1': { ticket: fx.ticket({ priority: 'high' }), visible: true } })
    panel(h).vm.$emit('patch', { priority: 'high' })
    await h.flush()
    expect(h.calls('PATCH /agent/tickets/t-1')[0]!.headers.get('If-Match')).toBeNull()
    expect(h.wrapper.find('[data-testid="priority-chip"]').text()).toContain('High')
  })

  it('a 403 keeps the server value and shows the problem title', async () => {
    const h = await open(fx.ticket(), { 'PATCH /agent/tickets/t-1': () => json({ title: 'Urgent needs a supervisor', status: 403 }, 403) })
    panel(h).vm.$emit('patch', { priority: 'urgent' })
    await h.flush()
    expect(useUiStore().toasts.at(-1)?.text).toBe('Urgent needs a supervisor')
    expect(h.wrapper.find('[data-testid="priority-chip"]').text()).toContain('Normal')
  })

  it('a 409 invalid-transition lists the allowed statuses; picking one PATCHes it', async () => {
    let n = 0
    const h = await open(fx.ticket(), {
      'PATCH /agent/tickets/t-1': () =>
        ++n === 1
          ? json({ type: 'https://featherdesk.dev/problems/invalid-transition', title: 'Invalid transition', status: 409, allowed: ['pending', 'resolved'] }, 409)
          : json({ ticket: fx.ticket({ status: 'resolved' }), visible: true }),
    })
    panel(h).vm.$emit('patch', { status: 'closed' })
    await h.flush()
    expect(allByTestId('transition-dialog')).toHaveLength(1)
    expect(byTestId('transition-closed')).toBeNull()
    await click(byTestId('transition-resolved'))
    await h.flush()
    expect(h.calls('PATCH /agent/tickets/t-1').map((c) => c.body)).toEqual([{ status: 'closed' }, { status: 'resolved' }])
  })

  it('a 412 opens the "changed by someone else" dialog and keeps the draft', async () => {
    const h = await open(fx.ticket(), {
      'PATCH /agent/tickets/t-1': () => json({ type: 'https://featherdesk.dev/problems/precondition-failed', title: 'Changed', status: 412 }, 412),
      'GET /agent/tickets/t-1': () => json(fx.ticket({ priority: 'high' }), 200, { ETag: 'W/"2"' }),
    })
    await typeInto(byTestId('composer-body'), 'my draft')
    panel(h).vm.$emit('patch', { priority: 'low' })
    await h.flush()
    expect(byTestId('stale-dialog')).toBeTruthy()
    const before = h.calls('GET /agent/tickets/t-1').length
    await click(byTestId('stale-reload'))
    await h.flush()
    expect(h.calls('GET /agent/tickets/t-1')).toHaveLength(before + 1)
    expect(byTestId('stale-dialog')?.closest('.v-overlay--active')).toBeFalsy()
    expect((byTestId('composer-body')!.querySelector('textarea') as HTMLTextAreaElement).value).toBe('my draft')
  })

  it('a team transfer answering visible: false navigates to the list with a toast', async () => {
    const h = await open(fx.ticket(), { 'PATCH /agent/tickets/t-1': { ticket: fx.ticket({ team_id: 'team-2' }), visible: false } })
    panel(h).vm.$emit('patch', { team_id: 'team-2' })
    await h.flush()
    expect(h.router.currentRoute.value.path).toBe('/agent/tickets')
    expect(useUiStore().toasts.at(-1)?.text).toBe('You no longer have access to this ticket.')
  })

  it('resolving a parent with an open child shows a non-blocking warning and still PATCHes', async () => {
    const link = { id: 'l1', type: 'parent_of', from_ticket_id: 't-1', to_ticket_id: 't-2', created_at: fx.iso, to_ticket: { id: 't-2', number: 1043, ref: 'WEB-1043', subject: 'Child', status: 'open' } } as const
    const h = await open(fx.ticket({ links: [link] }), { 'PATCH /agent/tickets/t-1': { ticket: fx.ticket({ status: 'resolved', links: [link] }), visible: true } })
    expect(byTestId('badge-parent')).toBeTruthy()
    panel(h).vm.$emit('patch', { status: 'resolved' })
    await h.flush()
    expect(useUiStore().toasts.some((t) => t.color === 'warning' && t.text.includes('1 open child'))).toBe(true)
    expect(h.calls('PATCH /agent/tickets/t-1')).toHaveLength(1)
  })
})

describe('TicketDetailPage — escalation and realtime (FE-024, FE-044)', () => {
  it('escalate without a reason sends nothing; with a reason and visible: false it navigates away', async () => {
    const h = await open(fx.ticket(), { 'POST /agent/tickets/t-1/escalate': { ticket: fx.ticket({ tier: 2 }), visible: false } })
    await click(byTestId('escalate-open'))
    const form = document.querySelector('[data-testid="escalate-form"]') as HTMLFormElement
    form.dispatchEvent(new Event('submit', { cancelable: true }))
    await h.flush()
    expect(h.calls('POST /agent/tickets/t-1/escalate')).toHaveLength(0)
    await typeInto(byTestId('escalate-reason'), 'Needs database access')
    form.dispatchEvent(new Event('submit', { cancelable: true }))
    await h.flush()
    expect(h.calls('POST /agent/tickets/t-1/escalate')[0]!.body).toEqual({ reason: 'Needs database access' })
    expect(h.router.currentRoute.value.path).toBe('/agent/tickets')
  })

  it('a message.created frame appends to the thread without HTTP', async () => {
    const h = await open()
    const before = h.calls().length
    h.socket().push({
      type: 'event', at: fx.iso, topic: 'ticket:t-1', event: 'message.created', event_id: 'e5',
      data: { message: { id: 'm-5', ticket_id: 't-1', kind: 'public', body_md: 'Any news?', body_html: '<p>Any news?</p>', created_at: fx.iso, attachments: [], author: { id: 'c-1', name: 'Ada Customer', kind: 'customer' } } },
    })
    await h.flush()
    expect(h.wrapper.text()).toContain('Any news?')
    expect(h.calls().length).toBe(before)
    expect(h.socket().frames('subscribe').some((f) => (f.topics as string[]).includes('ticket:t-1'))).toBe(true)
  })

  it('ticket.access_lost navigates back to the list', async () => {
    const h = await open()
    h.socket().push({ type: 'event', at: '2026-10-20T10:00:00Z', topic: 'ticket:t-1', event: 'ticket.access_lost', event_id: 'e6' })
    await h.flush()
    expect(h.router.currentRoute.value.path).toBe('/agent/tickets')
  })
})

describe('TicketDetailPage — presence (FE-046)', () => {
  it('typing for 10 s sends at most 4 presence frames', async () => {
    const h = await open()
    const ta = () => byTestId('composer-body')!.querySelector('textarea') as HTMLTextAreaElement
    const before = h.socket().frames('presence').length // the initial `viewing`
    vi.useFakeTimers({ now: Date.now() })
    for (let i = 0; i < 20; i++) {
      ta().value = 'x'.repeat(i + 1)
      ta().dispatchEvent(new Event('input', { bubbles: true }))
      await vi.advanceTimersByTimeAsync(500)
    }
    vi.useRealTimers()
    const typing = h.socket().frames('presence').filter((f) => f.state === 'typing')
    expect(typing.length).toBeGreaterThan(0)
    expect(h.socket().frames('presence').length - before).toBeLessThanOrEqual(4)
  })

  it('another agent typing shows the "is replying" warning', async () => {
    const h = await open()
    h.socket().push({ type: 'presence', topic: 'ticket:t-1', users: [{ id: 'u-me', name: 'Alice Agent', state: 'viewing' }, { id: 'u-2', name: 'Bob Builder', state: 'typing' }] })
    await h.flush()
    expect(byTestId('typing-warning')?.textContent).toContain('Bob Builder is replying')
    expect(h.wrapper.find('[data-testid="presence"]').exists()).toBe(true)
  })
})
