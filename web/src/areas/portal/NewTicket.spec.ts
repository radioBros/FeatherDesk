// SPDX-License-Identifier: AGPL-3.0-only

import NewTicketPage from './pages/NewTicketPage.vue'
import { mockFetch, json } from '@/test/mount'
import { DEFAULT_BRANDING } from '@/stores/config'
import { mountPage, requests, settle, ORG_MANAGER } from './testing'

afterEach(() => vi.unstubAllGlobals())

const CATEGORIES = [
  { id: 'c1', name: 'Billing', active: true, sort: 1, default_priority: null },
  { id: 'c2', name: 'Hardware', active: true, sort: 2, default_priority: 'high' },
]
const created = { id: 't-new', number: 7, subject: 'Printer', status: 'open', priority: 'normal', requester: { id: 'u', name: 'C', kind: 'customer' }, messages: [], allowed_actions: [], created_at: 'x', updated_at: 'x' }

function api(over: Record<string, () => Response> = {}) {
  return mockFetch((url, init) => {
    const path = new URL(url, 'http://x').pathname.replace('/api/v1', '')
    const method = (init.method ?? 'GET').toUpperCase()
    const key = `${method} ${path}`
    if (over[key]) return over[key]!()
    if (key === 'GET /portal/categories') return json(CATEGORIES)
    if (key === 'GET /portal/custom-fields') return json([])
    if (key === 'GET /portal/organization/members') return json([{ id: 'u-mgr', name: 'Mario', kind: 'customer' }, { id: 'u-col', name: 'Colleague', kind: 'customer' }])
    if (key === 'POST /portal/tickets') return json(created, 201)
    return json([])
  })
}

async function fill(wrapper: Awaited<ReturnType<typeof mountPage>>['wrapper']) {
  await wrapper.find('[data-testid="nt-subject"] input').setValue('Printer')
  await wrapper.find('[data-testid="md-input"] textarea').setValue('It is broken')
}

describe('portal new ticket (FE-016)', () => {
  it('an empty submit sends no POST', async () => {
    const { fn } = api()
    const { wrapper } = await mountPage(NewTicketPage, { path: '/tickets/new' })
    await settle(6)
    const before = fn.mock.calls.length
    await wrapper.find('[data-testid="new-ticket-form"]').trigger('submit')
    await settle()
    expect(fn.mock.calls.length).toBe(before)
    expect(wrapper.text()).toContain('This field is required')
  })

  it('creates with an Idempotency-Key and opens /tickets/:id', async () => {
    const { fn } = api()
    const { wrapper, router } = await mountPage(NewTicketPage, {
      path: '/tickets/new',
      config: { branding: { ...DEFAULT_BRANDING, new_ticket_intro_md: 'Please include your *order number*' } },
    })
    await settle(6)
    expect(wrapper.find('[data-testid="new-ticket-intro"]').html()).toContain('<em>order number</em>')
    await fill(wrapper)
    await wrapper.find('[data-testid="new-ticket-form"]').trigger('submit')
    await settle(8)
    const post = (await requests(fn)).find((r) => r.method === 'POST')!
    expect(post.path).toBe('/portal/tickets')
    expect(post.body).toMatchObject({ subject: 'Printer', body_md: 'It is broken', priority: 'normal', attachment_ids: [] })
    expect(post.headers.get('Idempotency-Key')).toBeTruthy()
    expect(post.headers.get('X-CSRF-Token')).toBe('csrf-1')
    expect(router.currentRoute.value.path).toBe('/tickets/t-new')
  })

  it('a 422 from the server shows on the subject field', async () => {
    api({ 'POST /portal/tickets': () => json({ type: 'x/validation', title: 'Invalid', status: 422, errors: [{ field: 'subject', code: 'max_length', params: { n: 200 } }] }, 422) })
    const { wrapper, router } = await mountPage(NewTicketPage, { path: '/tickets/new' })
    await settle(6)
    await fill(wrapper)
    await wrapper.find('[data-testid="new-ticket-form"]').trigger('submit')
    await settle(8)
    expect(wrapper.find('[data-testid="nt-subject"]').text()).toContain('Enter at most 200 characters')
    expect(router.currentRoute.value.path).toBe('/tickets/new')
  })

  it('offers urgent only when the instance allows it', async () => {
    api()
    const off = await mountPage(NewTicketPage, { path: '/tickets/new', config: { customer_urgent_allowed: false } })
    await settle(6)
    await off.wrapper.find('[data-testid="nt-priority"] .v-field').trigger('mousedown')
    await settle()
    expect(document.body.textContent).not.toContain('Urgent')
    off.wrapper.unmount()
    const on = await mountPage(NewTicketPage, { path: '/tickets/new', config: { customer_urgent_allowed: true } })
    await settle(6)
    await on.wrapper.find('[data-testid="nt-priority"] .v-field').trigger('mousedown')
    await settle()
    expect(document.body.textContent).toContain('Urgent')
    on.wrapper.unmount()
  })

  it('org managers may pick a colleague as requester', async () => {
    const { fn } = api()
    const { wrapper } = await mountPage(NewTicketPage, { path: '/tickets/new', me: ORG_MANAGER })
    await settle(6)
    expect((await requests(fn)).some((r) => r.path === '/portal/organization/members')).toBe(true)
    expect(wrapper.find('[data-testid="nt-requester"]').exists()).toBe(true)
  })
})

describe('portal new ticket: project picker (D-20)', () => {
  const proj = (id: string, key: string) => ({ id, key, name: `${key} project`, color: '#1976d2', description: '', icon: '', logo_url: null, portal_intro_md: null, portal_title: null, primary_color: null })

  it('is hidden with a single project, which is sent implicitly', async () => {
    const { fn } = api({ 'GET /portal/projects': () => json([proj('p-web', 'WEB')]) })
    const { wrapper } = await mountPage(NewTicketPage, { path: '/tickets/new' })
    await settle(8)
    expect(wrapper.find('[data-testid="nt-project"]').exists()).toBe(false)
    await fill(wrapper)
    await wrapper.find('[data-testid="new-ticket-form"]').trigger('submit')
    await settle(6)
    const post = (await requests(fn)).find((r) => r.method === 'POST' && r.path === '/portal/tickets')!
    expect((post.body as Record<string, unknown>).project_id).toBe('p-web')
  })

  it('shows the picker with several projects, preselects ?project=KEY and scopes categories to it', async () => {
    const { fn } = api({ 'GET /portal/projects': () => json([proj('p-web', 'WEB'), proj('p-mob', 'MOB')]) })
    const { wrapper } = await mountPage(NewTicketPage, { path: '/tickets/new?project=mob', pattern: '/tickets/new' })
    await settle(8)
    expect(wrapper.find('[data-testid="nt-project"]').exists()).toBe(true)
    const cat = (await requests(fn)).filter((r) => r.path === '/portal/categories').at(-1)!
    expect(new URLSearchParams(cat.search).get('project')).toBe('p-mob')
    await fill(wrapper)
    await wrapper.find('[data-testid="new-ticket-form"]').trigger('submit')
    await settle(6)
    const post = (await requests(fn)).find((r) => r.method === 'POST' && r.path === '/portal/tickets')!
    expect((post.body as Record<string, unknown>).project_id).toBe('p-mob')
  })
})
