// SPDX-License-Identifier: AGPL-3.0-only

import OrganizationsPage from './OrganizationsPage.vue'
import OrganizationDetailPage from './OrganizationDetailPage.vue'
import OrganizationForm from './OrganizationForm.vue'
import { mountAgent, fx, json } from '../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const org = (extra = {}) => ({ id: 'org-1', name: 'Acme', status: 'active' as const, created_at: fx.iso, updated_at: fx.iso, email_domains: ['acme.test'], member_count: 3, ...extra })

describe('OrganizationsPage (FE-026)', () => {
  it('lists organizations with domains and member count', async () => {
    const h = await mountAgent(OrganizationsPage, { api: { 'GET /agent/organizations': fx.page([org()]) } })
    await h.flush()
    const row = h.wrapper.find('[data-testid="org-row"]')
    expect(row.text()).toContain('Acme')
    expect(row.text()).toContain('acme.test')
    expect(row.text()).toContain('3')
  })
  it('shows the empty state', async () => {
    const h = await mountAgent(OrganizationsPage, { api: { 'GET /agent/organizations': fx.page([]) } })
    await h.flush()
    expect(h.wrapper.find('[data-testid="empty-state"]').exists()).toBe(true)
  })
})

describe('OrganizationDetailPage (FE-026)', () => {
  it('loads members and tickets scoped to the organization', async () => {
    const h = await mountAgent(OrganizationDetailPage, {
      path: '/agent/organizations/:id',
      route: '/agent/organizations/org-1',
      api: {
        'GET /agent/organizations/org-1': org({ notes: 'VIP' }),
        'GET /agent/customers': fx.page([fx.user({ organization_id: 'org-1', role: 'org_manager' })]),
        'GET /agent/tickets': fx.page([fx.summary()]),
      },
    })
    await h.flush()
    expect(h.calls('GET /agent/customers')[0]!.url.searchParams.get('organization_id')).toBe('org-1')
    expect(h.calls('GET /agent/tickets')[0]!.url.searchParams.get('organization_id')).toBe('org-1')
    expect(h.wrapper.find('[data-testid="org-members"]').text()).toContain('Ada Customer')
    expect(h.wrapper.text()).toContain('VIP')
  })
})

describe('OrganizationForm (FE-005, FE-026)', () => {
  it('an empty submit sends nothing', async () => {
    const h = await mountAgent(OrganizationForm)
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })
  it('rejects a chip that is not a domain via validations.domain', async () => {
    const h = await mountAgent(OrganizationForm, { props: { organization: org({ email_domains: ['acme.test', 'not a domain'] }) } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('Enter a valid domain (example.com)')
    expect(h.calls()).toHaveLength(0)
  })
  it('creates with normalised, de-duplicated domains', async () => {
    let body: any = null
    const h = await mountAgent(OrganizationForm, {
      api: {
        'POST /agent/organizations': (_u, init) => {
          body = init.body
          return json(org(), 201)
        },
      },
    })
    const vm = h.wrapper.findComponent({ name: 'VCombobox' })
    await h.wrapper.find('[data-testid="org-name"] input').setValue('Acme')
    vm.vm.$emit('update:modelValue', ['ACME.test', 'acme.test ', 'acme.io'])
    await h.flush()
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(body).toMatchObject({ name: 'Acme', email_domains: ['acme.test', 'acme.io'] })
    expect(h.wrapper.emitted('saved')).toBeTruthy()
  })
})
