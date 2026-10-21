// SPDX-License-Identifier: AGPL-3.0-only

import TicketDetailPage from './TicketDetailPage.vue'
import TicketNewPage from './TicketNewPage.vue'
import TimelineTab from './detail/TimelineTab.vue'
import ComposerForm from './detail/ComposerForm.vue'
import MergeForm from './detail/MergeForm.vue'
import SplitForm from './detail/SplitForm.vue'
import CustomFieldsForm from './detail/CustomFieldsForm.vue'
import { slaState } from './detail/sla'
import { fillVars, mentionAt } from './detail/composer'
import { mountAgent, fx, json, byTestId, allByTestId, typeInto, click, type AgentHarness } from '../testing'
import type { components } from '@/api/schema'

type S = components['schemas']

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
  document.body.innerHTML = ''
})

const lookups = {
  'GET /agent/teams': [fx.team()],
  'GET /agent/categories': [fx.category()],
  'GET /agent/tags': [],
  'GET /agent/staff': [fx.staffRef({ id: 'u-2', name: 'Bob Builder' }), fx.staffRef({ id: 'u-3', name: 'Bea Baker' })],
}
const planField: S['CustomFieldDef'] = {
  id: 'cf-1', key: 'plan', type: 'select', required: true, active: true, sort: 1, customer_visible: true, customer_editable: false,
  label: { en: 'Plan' }, options: [{ value: 'gold', label: { en: 'Gold' } }, { value: 'free', label: { en: 'Free' } }], category_ids: null,
}

function openDetail(ticket: S['Ticket'], api: Record<string, unknown> = {}) {
  return mountAgent(TicketDetailPage, {
    path: '/agent/tickets/:number',
    route: `/agent/tickets/${ticket.number}`,
    api: { ...lookups, 'GET /agent/custom-fields': [], [`GET /agent/tickets/by-number/${ticket.number}`]: ticket, ...api } as never,
  })
}

const ALL_TYPES: S['EventType'][] = [
  'ticket.created', 'ticket.status_changed', 'ticket.priority_changed', 'ticket.assigned', 'ticket.team_changed', 'ticket.escalated',
  'ticket.deescalated', 'ticket.category_changed', 'ticket.tags_changed', 'ticket.fields_changed', 'message.created', 'note.created',
  'sla.warning', 'sla.breached', 'ticket.csat', 'ticket.followup_created', 'ticket.merged', 'ticket.split', 'ticket.linked',
  'ticket.unlinked', 'ticket.children_resolved',
]

describe('Timeline (FE-022)', () => {
  it('renders every event type with a label, the escalation reason, and a fallback for unknown types', async () => {
    const items = [...ALL_TYPES, 'ticket.future_thing' as S['EventType']].map((type, i) => ({
      id: `e${i}`, ticket_id: 't-1', type, created_at: fx.iso, actor: { id: 'u-me', name: 'Alice Agent', kind: 'staff' as const },
      data: type === 'ticket.escalated' ? { from_tier: 1, to_tier: 2, reason: 'Needs DB access' } : type === 'ticket.status_changed' ? { from: 'open', to: 'pending' } : {},
    }))
    const h = await mountAgent(TimelineTab, { props: { ticketId: 't-1' }, api: { 'GET /agent/tickets/t-1/events': fx.page(items) } })
    const rows = h.wrapper.findAll('[data-testid="timeline-item"]')
    expect(rows).toHaveLength(ALL_TYPES.length + 1)
    for (const r of rows.slice(0, ALL_TYPES.length)) expect(r.text()).not.toMatch(/agent\.ticket\.events|Event ticket/)
    expect(h.wrapper.text()).toContain('Reason: Needs DB access')
    expect(h.wrapper.text()).toContain('Open → Pending')
    expect(rows.at(-1)!.text()).toContain('Event ticket.future_thing')
  })
})

describe('Composer extras (FE-039, FE-052)', () => {
  const kb = fx.page([
    { id: 'a1', slug: 'reset-password', title: 'Reset your password', locale: 'en', visibility: 'public', updated_at: fx.iso },
    { id: 'a2', slug: 'internal-runbook', title: 'DB runbook', locale: 'en', visibility: 'internal', updated_at: fx.iso },
  ])

  async function mountComposer(api: Record<string, unknown> = {}): Promise<AgentHarness> {
    return mountAgent(ComposerForm, {
      props: { ticket: { id: 't-1', number: 1042, allowed_actions: ['reply', 'note', 'set_status'], requester: { id: 'c-1', name: 'Ada Customer', kind: 'customer' } } },
      api: { 'GET /agent/kb/articles': kb, ...lookups, ...api } as never,
    })
  }

  it('in a public reply the KB picker never lists internal articles; in a note it does', async () => {
    const h = await mountComposer()
    await click(byTestId('kb-link-open'))
    await h.flush()
    expect(h.calls('GET /agent/kb/articles')[0]!.url.searchParams.get('status')).toBe('published')
    expect(allByTestId('kb-link-item').map((e) => e.textContent)).toEqual([expect.stringContaining('Reset your password')])
    ;(h.wrapper.vm as unknown as { setMode: (m: string) => void }).setMode('internal')
    await h.flush()
    expect(allByTestId('kb-link-item')).toHaveLength(2)
  })

  it('an internal link inserted in a note blocks the send once switched to a public reply', async () => {
    const h = await mountComposer({ 'POST /agent/tickets/t-1/messages': () => json({ id: 'm', ticket_id: 't-1', kind: 'public', body_md: '', created_at: fx.iso, attachments: [] }, 201) })
    const vm = h.wrapper.vm as unknown as { setMode: (m: string) => void }
    vm.setMode('internal')
    await h.flush()
    await click(byTestId('kb-link-open'))
    await h.flush()
    await click(allByTestId('kb-link-item').find((e) => e.textContent?.includes('DB runbook'))!)
    await h.flush()
    vm.setMode('public')
    await h.flush()
    expect(byTestId('public-warning')).toBeTruthy()
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('A public reply cannot link an internal knowledge base article')
    expect(h.calls('POST /agent/tickets/t-1/messages')).toHaveLength(0)
  })

  it('a canned response fills its variables and is inserted at the cursor', async () => {
    const h = await mountComposer({ 'GET /agent/canned-responses': [{ id: 'c1', title: 'Greeting', body_md: 'Hi {{customer.name}}, re #{{ticket.number}} ({{requester.name}})' }] })
    const ta = () => byTestId('composer-body')!.querySelector('textarea') as HTMLTextAreaElement
    await typeInto(byTestId('composer-body'), 'AB')
    ta().setSelectionRange(1, 1)
    await click(byTestId('canned-open'))
    await h.flush()
    await click(byTestId('canned-item'))
    await h.flush()
    expect(ta().value).toBe('AHi Ada Customer, re #1042 (Ada Customer)B')
    expect(ta().selectionStart).toBe('AHi Ada Customer, re #1042 (Ada Customer)'.length)
  })

  it('@mention suggestions in notes come from the staff directory', async () => {
    const h = await mountComposer()
    ;(h.wrapper.vm as unknown as { setMode: (m: string) => void }).setMode('internal')
    await h.flush()
    await typeInto(byTestId('composer-body'), 'ping @Be')
    await h.flush()
    expect(h.calls('GET /agent/staff')).toHaveLength(1)
    expect(byTestId('mention-list')?.textContent).toContain('Bea Baker')
  })

  it('helpers: variables and mention detection', () => {
    expect(fillVars('{{agent.name}} / {{unknown}}', { 'agent.name': 'Al' })).toBe('Al / {{unknown}}')
    expect(mentionAt('hello @bo', 9)).toBe('bo')
    expect(mentionAt('mail a@b', 8)).toBeNull()
  })
})

describe('Links, merge and split (FE-055)', () => {
  it('merge: a 409 merge-cross-requester shows the translated reason and stays open', async () => {
    const h = await mountAgent(MergeForm, {
      props: { ticket: { id: 't-1', number: 1042 } },
      api: { 'POST /agent/tickets/t-1/merge': () => json({ type: 'https://simpletickets.dev/problems/merge-cross-requester', title: 'x', status: 409 }, 409) },
    })
    ;(h.wrapper.vm as unknown as { $: { setupState: { target: unknown } } }).$.setupState.target = fx.summary({ id: 't-9', number: 1099 })
    await h.flush()
    expect(byTestId('merge-summary')?.textContent).toContain('#1099')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(byTestId('merge-problem')?.textContent).toContain('different customers')
    expect(h.wrapper.emitted('merged')).toBeUndefined()
  })

  it('merge success navigates to the target ticket', async () => {
    const h = await openDetail(fx.ticket({ allowed_actions: ['reply', 'merge'] }), {
      'POST /agent/tickets/t-1/merge': { ticket: fx.ticket({ status: 'closed' }), visible: true },
    })
    await click(byTestId('ticket-more'))
    await click(byTestId('merge-open'))
    const form = h.wrapper.findComponent(MergeForm)
    ;(form.vm as unknown as { $: { setupState: { target: unknown } } }).$.setupState.target = fx.summary({ id: 't-9', number: 1099 })
    await h.flush()
    ;(document.querySelector('[data-testid="merge-form"]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await h.flush()
    expect(h.calls('POST /agent/tickets/t-1/merge')[0]!.body).toEqual({ into_ticket_id: 't-9' })
    expect(h.router.currentRoute.value.path).toBe('/agent/tickets/1099')
  })

  it('split: without a selected message or subject nothing is sent; then it posts exactly the selected ids', async () => {
    const t = fx.ticket({
      allowed_actions: ['reply', 'split'],
      messages: [
        { id: 'm-1', ticket_id: 't-1', kind: 'public', body_md: 'one', created_at: fx.iso, attachments: [] },
        { id: 'm-2', ticket_id: 't-1', kind: 'public', body_md: 'two', created_at: fx.iso, attachments: [] },
        { id: 'm-3', ticket_id: 't-1', kind: 'internal', body_md: 'three', created_at: fx.iso, attachments: [] },
      ],
    })
    const h = await openDetail(t, { 'POST /agent/tickets/t-1/split': () => json(fx.ticket({ id: 't-5', number: 1050 }), 201), 'GET /agent/tickets/t-1': t })
    await click(byTestId('ticket-more'))
    await click(byTestId('split-open'))
    const form = h.wrapper.find('[data-testid="split-form"]')
    await form.trigger('submit')
    await h.flush()
    expect(h.calls('POST /agent/tickets/t-1/split')).toHaveLength(0)
    const boxes = h.wrapper.findAll('[data-testid="msg-select"] input')
    await boxes[0]!.setValue(true)
    await boxes[2]!.setValue(true)
    await typeInto(byTestId('split-subject'), 'Second problem')
    await form.trigger('submit')
    await h.flush()
    expect(h.calls('POST /agent/tickets/t-1/split')[0]!.body).toEqual({ subject: 'Second problem', message_ids: ['m-1', 'm-3'] })
    expect(byTestId('split-open-child')?.getAttribute('href')).toBe('/agent/tickets/1050')
  })

  it('split form: an empty selection is rejected client-side', async () => {
    const h = await mountAgent(SplitForm, { props: { ticketId: 't-1', selected: [] } })
    await typeInto(byTestId('split-subject'), 'x')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.calls()).toHaveLength(0)
  })

  it('the links panel groups children and adds a link through the form', async () => {
    const link = { id: 'l1', type: 'parent_of', from_ticket_id: 't-1', to_ticket_id: 't-2', created_at: fx.iso, to_ticket: { id: 't-2', number: 1043, subject: 'Child', status: 'open' } } as const
    const h = await openDetail(fx.ticket({ links: [link] }))
    expect(byTestId('links-panel')?.textContent).toContain('Child tickets')
    expect(byTestId('links-panel')?.textContent).toContain('#1043')
    await click(byTestId('link-add'))
    const form = document.querySelector('[data-testid="link-form"]') as HTMLFormElement
    form.dispatchEvent(new Event('submit', { cancelable: true }))
    await h.flush()
    expect(h.calls(/^POST/)).toHaveLength(0)
  })
})

describe('Custom fields in the properties panel (FE-054)', () => {
  it('renders the applicable fields; read-only without edit_fields', async () => {
    const h = await mountAgent(TicketDetailPage, {
      path: '/agent/tickets/:number',
      route: '/agent/tickets/1042',
      api: { ...lookups, 'GET /agent/custom-fields': [planField], 'GET /agent/tickets/by-number/1042': fx.ticket({ allowed_actions: ['reply'], custom_fields: { plan: 'gold' } }) } as never,
    })
    await h.flush()
    expect(byTestId('custom-fields-form')).toBeTruthy()
    expect(byTestId('custom-fields-save')).toBeNull()
  })

  it('saves custom_fields through the form; a required empty field blocks the save', async () => {
    const save = vi.fn(async () => {})
    const h = await mountAgent(CustomFieldsForm, { props: { defs: [planField], values: {}, editable: true, save } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(save).not.toHaveBeenCalled()
    const h2 = await mountAgent(CustomFieldsForm, { props: { defs: [planField], values: { plan: 'gold' }, editable: true, save } })
    await h2.wrapper.find('form').trigger('submit')
    await h2.flush()
    expect(save).toHaveBeenCalledWith({ plan: 'gold' })
  })
})

describe('SLA badge states (FE-039)', () => {
  const start = '2026-10-20T10:00:00Z'
  const due = '2026-10-20T20:00:00Z'
  it.each([
    ['ok', { now: new Date('2026-10-20T12:00:00Z') }],
    ['warning', { now: new Date('2026-10-20T18:30:00Z') }],
    ['breached', { now: new Date('2026-10-20T21:00:00Z') }],
    ['paused', { pausedAt: '2026-10-20T19:00:00Z', now: new Date('2026-10-20T21:00:00Z') }],
    ['met', { metAt: '2026-10-20T15:00:00Z' }],
  ] as const)('%s', (state, extra) => {
    expect(slaState({ start, due, ...extra })).toBe(state)
  })
  it('none without a due date', () => expect(slaState({ start })).toBe('none'))
})

describe('TicketNewPage (FE-025)', () => {
  it('an empty create sends nothing; the new-customer path sends requester_email + requester_name and opens the ticket by number', async () => {
    const h = await mountAgent(TicketNewPage, {
      routes: [{ path: '/agent/tickets/:number' }],
      api: { ...lookups, 'GET /agent/custom-fields': [], 'POST /agent/tickets': () => json(fx.ticket({ number: 2001 }), 201) } as never,
    })
    await h.flush()
    const form = h.wrapper.find('[data-testid="ticket-create-form"]')
    await form.trigger('submit')
    await h.flush()
    expect(h.calls('POST /agent/tickets')).toHaveLength(0)
    await click(byTestId('new-customer-toggle')?.querySelector('input') ?? null)
    await typeInto(byTestId('requester-name'), 'Grace Hopper')
    await typeInto(byTestId('requester-email'), 'grace@example.com')
    await typeInto(byTestId('ticket-subject-input'), 'Compiler bug')
    await typeInto(byTestId('ticket-body'), 'It prints moths')
    await form.trigger('submit')
    await h.flush()
    const post = h.calls('POST /agent/tickets')[0]!
    expect(post.body).toMatchObject({ requester_email: 'grace@example.com', requester_name: 'Grace Hopper', subject: 'Compiler bug', body_md: 'It prints moths' })
    expect(post.body).not.toHaveProperty('requester_id')
    expect(post.headers.get('Idempotency-Key')).toBeTruthy()
    expect(h.router.currentRoute.value.path).toBe('/agent/tickets/2001')
  })
})
