// SPDX-License-Identifier: AGPL-3.0-only

import CustomersPage from './CustomersPage.vue'
import CustomerDetailPage from './CustomerDetailPage.vue'
import CustomerForm from './CustomerForm.vue'
import { mountAgent, fx, json, byTestId, typeInto, click } from '../testing'
import { scrollTableToEnd } from '@/test/mount'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const org = { id: 'org-1', name: 'Acme', status: 'active' as const, created_at: fx.iso, updated_at: fx.iso, email_domains: ['acme.test'] }

describe('CustomersPage (FE-026)', () => {
  it('lists customers with their organization and searches with q', async () => {
    const h = await mountAgent(CustomersPage, {
      api: {
        'GET /agent/customers': fx.page([fx.user({ organization_id: 'org-1' }), fx.user({ id: 'c-2', name: 'Bo', email: 'bo@x.test' })], 'next-1'),
        'GET /agent/organizations': fx.page([org]),
      },
    })
    await h.flush()
    const rows = h.wrapper.findAll('[data-testid="customer-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.text()).toContain('Acme')
    // More pages load when the table body is scrolled to its end.
    scrollTableToEnd(h.wrapper.element)
    await h.flush()
    expect(h.calls(/GET \/agent\/customers\?.*cursor=next-1/)).toHaveLength(1)
    vi.useFakeTimers()
    await h.wrapper.find('[data-testid="customer-search"] input').setValue('ada')
    vi.advanceTimersByTime(350)
    vi.useRealTimers()
    await h.flush()
    expect(h.calls(/GET \/agent\/customers\?.*q=ada/)).toHaveLength(1)
  })

  it('creates a customer and opens it', async () => {
    let body: any = null
    const h = await mountAgent(CustomersPage, {
      routes: [{ path: '/agent/customers/:id' }],
      api: {
        'GET /agent/customers': fx.page([]),
        'GET /agent/organizations': fx.page([org]),
        'POST /agent/customers': (_u, init) => {
          body = init.body
          return json(fx.user({ id: 'c-9', name: 'Zed', email: 'zed@x.test' }), 201)
        },
      },
    })
    await click(byTestId('customer-new'))
    await typeInto(byTestId('customer-name'), 'Zed')
    await typeInto(byTestId('customer-email'), 'zed@x.test')
    await click(byTestId('customer-save'))
    await h.flush()
    expect(body).toMatchObject({ name: 'Zed', email: 'zed@x.test', role: 'requester', send_invite: false })
    expect(h.router.currentRoute.value.path).toBe('/agent/customers/c-9')
  })
})

describe('CustomerDetailPage (FE-026)', () => {
  it('shows the customer and their tickets through the visibility-scoped list API', async () => {
    const h = await mountAgent(CustomerDetailPage, {
      path: '/agent/customers/:id',
      route: '/agent/customers/c-1',
      api: {
        'GET /agent/customers/c-1': fx.user({ organization_id: 'org-1' }),
        'GET /agent/organizations': fx.page([org]),
        'GET /agent/tickets': fx.page([fx.summary()]),
      },
    })
    await h.flush()
    expect(h.wrapper.text()).toContain('Ada Customer')
    expect(h.wrapper.text()).toContain('Acme')
    const req = h.calls('GET /agent/tickets')[0]!
    expect(req.url.searchParams.get('requester_id')).toBe('c-1')
    const link = h.wrapper.find('[data-testid="mini-ticket-row"] a')
    expect(link.attributes('href')).toBe('/agent/tickets/WEB-1042')
  })

  it('shows the problem when the customer is not visible', async () => {
    const h = await mountAgent(CustomerDetailPage, {
      path: '/agent/customers/:id',
      route: '/agent/customers/nope',
      api: { 'GET /agent/organizations': fx.page([]) },
    })
    await h.flush()
    expect(h.wrapper.find('[data-testid="load-error"]').exists()).toBe(true)
  })
})

describe('CustomerForm (FE-005)', () => {
  it('an empty submit shows the required message and sends nothing', async () => {
    const h = await mountAgent(CustomerForm, { props: { organizations: [] } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })
  it('an invalid email is rejected client-side', async () => {
    const h = await mountAgent(CustomerForm, { props: { organizations: [] } })
    await typeInto(byTestId('customer-name'), 'Zed')
    await typeInto(byTestId('customer-email'), 'not-an-email')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('Enter a valid email address')
    expect(h.calls()).toHaveLength(0)
  })
  it('editing PATCHes the customer without the email', async () => {
    const h = await mountAgent(CustomerForm, {
      props: { customer: fx.user(), organizations: [] },
      api: { 'PATCH /agent/customers/c-1': fx.user() },
    })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    const body = h.calls('PATCH /agent/customers/c-1')[0]!.body as Record<string, unknown>
    expect(body).toMatchObject({ name: 'Ada Customer', role: 'requester', status: 'active' })
    expect(body).not.toHaveProperty('email')
  })
})
