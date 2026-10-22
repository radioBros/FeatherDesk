// SPDX-License-Identifier: AGPL-3.0-only

import TokensPage from './TokensPage.vue'
import TokenForm from './TokenForm.vue'
import AllTokensPage from './admin/AllTokensPage.vue'
import { settleConfirm } from '@/composables/useConfirm'
import { useRealtimeStore } from '@/stores/realtime'
import { mountAgent, fx, json, byTestId, typeInto, click } from '../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const SECRET = 'fd_pat_SUPERSECRETVALUE0123456789'
const token = (extra = {}) => ({ id: 'tok-1', name: 'CI', prefix: 'fd_pat_S', scopes: ['tickets:read' as const], created_at: fx.iso, last_used_at: null, expires_at: null, ...extra })

describe('TokensPage (FE-030)', () => {
  it('shows a new token once; after closing it is gone from the DOM and from Pinia', async () => {
    let body: any = null
    const h = await mountAgent(TokensPage, {
      api: {
        'GET /me/tokens': [],
        'POST /me/tokens': (_u, init) => {
          body = init.body
          return json({ ...token({ name: 'CI' }), token: SECRET }, 201)
        },
      },
    })
    await click(byTestId('token-new'))
    await typeInto(byTestId('token-name'), 'CI')
    await click(byTestId('token-create'))
    await h.flush()
    expect(body).toMatchObject({ name: 'CI', scopes: ['tickets:read'], expires_at: null })
    expect(document.body.textContent + (byTestId('secret-value')?.querySelector('input')?.value ?? '')).toContain(SECRET)
    expect(h.wrapper.findAll('[data-testid="token-row"]')).toHaveLength(1)

    await click(byTestId('token-created-close'))
    await h.flush()
    expect(document.body.innerHTML).not.toContain(SECRET)
    expect(byTestId('secret-value')).toBeNull()
    expect(JSON.stringify(useRealtimeStore().$state)).not.toContain(SECRET)
    const pinia = h.wrapper.vm.$pinia
    expect(JSON.stringify(pinia.state.value)).not.toContain(SECRET)
  })

  it('revokes after confirmation', async () => {
    const h = await mountAgent(TokensPage, {
      api: { 'GET /me/tokens': [token()], 'DELETE /me/tokens/tok-1': new Response(null, { status: 204 }) },
    })
    await h.flush()
    await click(h.wrapper.find('[data-testid="token-revoke"]').element)
    settleConfirm(true)
    await h.flush()
    expect(h.calls('DELETE /me/tokens/tok-1')).toHaveLength(1)
    expect(h.wrapper.findAll('[data-testid="token-row"]')).toHaveLength(0)
  })

  it('offers the admin scope only to administrators', async () => {
    const agent = await mountAgent(TokensPage, { api: { 'GET /me/tokens': [] } })
    await click(byTestId('token-new'))
    expect(byTestId('scope-tickets:write')).not.toBeNull()
    expect(byTestId('scope-admin')).toBeNull()
    agent.wrapper.unmount()
    document.body.innerHTML = ''
    await mountAgent(TokensPage, { me: fx.me('admin'), api: { 'GET /me/tokens': [] } })
    await click(byTestId('token-new'))
    expect(byTestId('scope-admin')).not.toBeNull()
  })
})

describe('TokenForm (FE-005)', () => {
  it('an empty submit sends nothing', async () => {
    const h = await mountAgent(TokenForm)
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })
  it('requires at least one scope and a valid expiry date', async () => {
    const h = await mountAgent(TokenForm)
    await typeInto(byTestId('token-name'), 'CI')
    await h.wrapper.find('[data-testid="scope-tickets:read"] input').setValue(false)
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.find('[data-testid="token-scopes"]').text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })
  it('sends the expiry as the end of the chosen day', async () => {
    const h = await mountAgent(TokenForm, { api: { 'POST /me/tokens': json({ ...token(), token: SECRET }, 201) } })
    await typeInto(byTestId('token-name'), 'CI')
    await typeInto(byTestId('token-expiry'), '2027-01-31')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.calls('POST /me/tokens')[0]!.body).toMatchObject({ expires_at: '2027-01-31T23:59:59Z' })
    expect(h.wrapper.emitted('created')).toBeTruthy()
  })
})

describe('TokenForm: tickets:act_as_customer (I-12)', () => {
  it('is offered to admins only, with the trusted-integration warning when checked', async () => {
    const agent = await mountAgent(TokenForm)
    expect(agent.wrapper.find('[data-testid="scope-tickets:act_as_customer"]').exists()).toBe(false)
    agent.wrapper.unmount()
    const h = await mountAgent(TokenForm, {
      props: { allowAdmin: true },
      api: { 'POST /me/tokens': json({ ...token(), token: SECRET }, 201) },
    })
    expect(h.wrapper.find('[data-testid="act-as-customer-warning"]').exists()).toBe(false)
    await h.wrapper.find('[data-testid="scope-tickets:act_as_customer"] input').setValue(true)
    await h.flush()
    expect(h.wrapper.find('[data-testid="act-as-customer-warning"]').text()).toBe(
      'Lets this token post messages as any customer. Give it only to trusted integrations.',
    )
    await typeInto(byTestId('token-name'), 'Verbalizza')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.calls('POST /me/tokens')[0]!.body).toMatchObject({ scopes: ['tickets:read', 'tickets:act_as_customer'] })
  })
})

describe('AllTokensPage (FE-042)', () => {
  it('lists every token with its owner and revokes after confirmation', async () => {
    const h = await mountAgent(AllTokensPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/tokens': fx.page([token({ user: { id: 'u-9', name: 'Owner Olga', kind: 'staff' } })]),
        'DELETE /admin/tokens/tok-1': new Response(null, { status: 204 }),
      },
    })
    await h.flush()
    const row = h.wrapper.find('[data-testid="all-token-row"]')
    expect(row.text()).toContain('Owner Olga')
    await click(row.find('[data-testid="all-token-revoke"]').element)
    settleConfirm(true)
    await h.flush()
    expect(h.calls('DELETE /admin/tokens/tok-1')).toHaveLength(1)
    expect(h.wrapper.find('[data-testid="token-revoked"]').exists()).toBe(true)
  })
})
