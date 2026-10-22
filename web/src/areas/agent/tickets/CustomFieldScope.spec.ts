// SPDX-License-Identifier: AGPL-3.0-only

import TicketCreateForm from './detail/TicketCreateForm.vue'
import TicketDetailPage from './TicketDetailPage.vue'
import { mountAgent, fx, json, byTestId, typeInto, type AgentHarness } from '../testing'
import type { components } from '@/api/schema'

type S = components['schemas']

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

// The demo field: text, required, customer-editable, scoped to Billing.
const orderNumber: S['CustomFieldDef'] = {
  id: 'cf-order', key: 'order_number', type: 'text', required: true, active: true, sort: 1, customer_visible: true, customer_editable: true,
  label: { en: 'Order number' }, options: [], category_ids: ['cat-billing'],
}
const appVersion: S['CustomFieldDef'] = {
  id: 'cf-app', key: 'app_version', type: 'text', required: false, active: true, sort: 2, customer_visible: true, customer_editable: true,
  label: { en: 'App version' }, options: [], category_ids: null, project_ids: ['p-mob'],
}
const categories = [fx.category({ id: 'cat-billing', name: 'Billing' }), fx.category({ id: 'cat-tech', name: 'Technical' })]

type FormState = { model: { category_id: string | null; custom: Record<string, unknown> } }
const state = (h: AgentHarness) => (h.wrapper.vm as unknown as { $: { setupState: FormState } }).$.setupState

async function mountCreate() {
  const h = await mountAgent(TicketCreateForm, {
    props: { categories, customFields: [orderNumber, appVersion], requester: fx.user({ id: 'c-1' }), defaultProjectId: 'p-web' },
    api: { 'POST /agent/tickets': () => json(fx.ticket({ number: 7 }), 201) } as never,
  })
  await h.flush()
  await typeInto(byTestId('ticket-subject-input'), 'Invoice question')
  await typeInto(byTestId('ticket-body'), 'Please check')
  return h
}

async function submit(h: AgentHarness) {
  await h.wrapper.find('[data-testid="ticket-create-form"]').trigger('submit')
  await h.flush()
}

describe('agent new ticket: custom fields follow the category', () => {
  it('without a category the Billing-only order_number is not shown and not sent', async () => {
    const h = await mountCreate()
    expect(byTestId('cf-order_number')).toBeNull()
    // Another project's field never shows either.
    expect(byTestId('cf-app_version')).toBeNull()
    await submit(h)
    const [post] = h.calls('POST /agent/tickets')
    expect(post).toBeTruthy()
    expect(post!.body).not.toHaveProperty('custom_fields')
  })

  it('with category Billing order_number is required, then sent', async () => {
    const h = await mountCreate()
    state(h).model.category_id = 'cat-billing'
    await h.flush()
    expect(byTestId('cf-order_number')).not.toBeNull()
    await submit(h)
    expect(h.calls('POST /agent/tickets')).toHaveLength(0)
    await typeInto(byTestId('cf-order_number'), 'INV-7')
    await submit(h)
    expect(h.calls('POST /agent/tickets')[0]!.body).toMatchObject({ category_id: 'cat-billing', custom_fields: { order_number: 'INV-7' } })
  })

  it('switching away from Billing drops the order_number value from the payload', async () => {
    const h = await mountCreate()
    state(h).model.category_id = 'cat-billing'
    await h.flush()
    await typeInto(byTestId('cf-order_number'), 'INV-7')
    state(h).model.category_id = 'cat-tech'
    await h.flush()
    expect(byTestId('cf-order_number')).toBeNull()
    expect(state(h).model.custom).not.toHaveProperty('order_number')
    await submit(h)
    expect(h.calls('POST /agent/tickets')[0]!.body).not.toHaveProperty('custom_fields')
  })
})

describe('agent ticket detail: the custom fields editor follows the category', () => {
  const open = (ticket: S['Ticket']) =>
    mountAgent(TicketDetailPage, {
      path: '/agent/tickets/:number',
      route: `/agent/tickets/${ticket.number}`,
      api: {
        'GET /agent/teams': [fx.team()],
        'GET /agent/categories': categories,
        'GET /agent/tags': [],
        'GET /agent/staff': [],
        'GET /agent/custom-fields': [orderNumber],
        [`GET /agent/tickets/by-number/${ticket.number}`]: ticket,
      } as never,
    })

  it('a ticket without a category has no order_number input', async () => {
    const h = await open(fx.ticket({ category_id: null }))
    await h.flush()
    expect(byTestId('cf-order_number')).toBeNull()
  })

  it('a Billing ticket shows order_number as a required input', async () => {
    const h = await open(fx.ticket({ category_id: 'cat-billing' }))
    await h.flush()
    expect(byTestId('cf-order_number')).not.toBeNull()
    await h.wrapper.find('[data-testid="custom-fields-form"]').trigger('submit')
    await h.flush()
    expect(h.calls(/^PATCH/)).toHaveLength(0)
  })
})
