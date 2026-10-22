// SPDX-License-Identifier: AGPL-3.0-only

import MyDataPage from './pages/MyDataPage.vue'
import RegisterPage from './pages/RegisterPage.vue'
import { mockFetch, json } from '@/test/mount'
import { mountPage, requests, settle } from './testing'
import type { components } from '@/api/schema'

type S = components['schemas']
afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const ISO = '2026-10-20T10:00:00Z'
const base: S['PrivacyRequest'] = {
  id: 'r-1', type: 'export', status: 'ready', user_id: 'u-cust', requested_by: 'u-cust', include_internal: false, mode: null, file_size: 4096,
  reason: '', error: '', decided_by: null, decided_at: null, completed_at: ISO, expires_at: '2026-10-27T10:00:00Z',
  download_url: '/api/v1/me/privacy/exports/r-1', created_at: ISO,
}
const privacy = (extra: Partial<S['MyPrivacy']> = {}): S['MyPrivacy'] => ({
  requests: [base],
  consents: [{ id: 'c-1', kind: 'privacy_policy', policy_url: 'https://acme.test/privacy', policy_version: '2026-10', ip: '', accepted_at: ISO }],
  can_request_erasure: true,
  export_days: 7,
  ...extra,
})

describe('portal "My data" (FE-082)', () => {
  it('shows the ready export as a download link and the consent records', async () => {
    mockFetch(() => json(privacy()))
    const { wrapper } = await mountPage(MyDataPage, { path: '/profile/data' })
    await settle()
    const dl = wrapper.find('[data-testid="privacy-download"]')
    expect(dl.attributes('href')).toBe('/api/v1/me/privacy/exports/r-1')
    expect(dl.attributes('download')).toBeDefined()
    expect(wrapper.find('[data-testid="consent-row"]').text()).toContain('https://acme.test/privacy')
    expect(wrapper.text()).toContain('7 days')
  })

  it('requests an export and reloads the list', async () => {
    const { fn } = mockFetch((url, init) =>
      String(url).endsWith('/me/privacy/export') && init.method === 'POST' ? json({ ...base, status: 'pending', download_url: null }, 202) : json(privacy({ requests: [] })),
    )
    const { wrapper } = await mountPage(MyDataPage, { path: '/profile/data' })
    await settle()
    await wrapper.find('[data-testid="export-request"]').trigger('click')
    await settle(8)
    const reqs = await requests(fn)
    expect(reqs.map((r) => `${r.method} ${r.path}`)).toEqual(['GET /me/privacy', 'POST /me/privacy/export', 'GET /me/privacy'])
  })

  it('a deletion request needs the acknowledgement and sends the optional reason', async () => {
    const { fn } = mockFetch((url, init) =>
      init.method === 'POST' ? json({ ...base, type: 'erasure', status: 'pending', download_url: null }, 202) : json(privacy({ requests: [] })),
    )
    const { wrapper } = await mountPage(MyDataPage, { path: '/profile/data' })
    await settle()
    await wrapper.find('[data-testid="erasure-reason"] textarea').setValue('Closing my business')
    await wrapper.find('[data-testid="erasure-request-form"]').trigger('submit')
    await settle()
    expect((await requests(fn)).filter((r) => r.method === 'POST')).toHaveLength(0)
    expect(wrapper.text()).toContain('This field is required')
    await wrapper.find('[data-testid="erasure-confirm"] input').setValue(true)
    await wrapper.find('[data-testid="erasure-request-form"]').trigger('submit')
    await settle(8)
    const post = (await requests(fn)).find((r) => r.method === 'POST')
    expect(post).toMatchObject({ path: '/me/privacy/erasure', body: { reason: 'Closing my business' } })
  })

  it('a pending deletion request replaces the form', async () => {
    mockFetch(() => json(privacy({ requests: [{ ...base, type: 'erasure', status: 'pending', download_url: null }], can_request_erasure: false })))
    const { wrapper } = await mountPage(MyDataPage, { path: '/profile/data' })
    await settle()
    expect(wrapper.find('[data-testid="erasure-pending"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="erasure-request-form"]').exists()).toBe(false)
  })
})

describe('register consent (FE-081)', () => {
  const cfg = (privacy_url: string | null) => ({ email_enabled: true, branding: { privacy_url } }) as never

  it('requires accepting the privacy policy when a policy URL is configured', async () => {
    const { fn } = mockFetch(() => json({ ok: true }, 202))
    const { wrapper } = await mountPage(RegisterPage, { path: '/register', me: null, config: cfg('https://acme.test/privacy') })
    await settle()
    expect(wrapper.find('[data-testid="register-privacy-link"]').attributes('href')).toBe('https://acme.test/privacy')
    await wrapper.find('[data-testid="register-name"] input').setValue('Carla')
    await wrapper.find('[data-testid="register-email"] input').setValue('carla@example.com')
    await wrapper.find('[data-testid="register-password"] input').setValue('a-long-password-1')
    await wrapper.find('[data-testid="register-confirm"] input').setValue('a-long-password-1')
    await wrapper.find('[data-testid="register-form"]').trigger('submit')
    await settle()
    expect(fn).not.toHaveBeenCalled()
    await wrapper.find('[data-testid="register-privacy"] input').setValue(true)
    await wrapper.find('[data-testid="register-form"]').trigger('submit')
    await settle(8)
    const [post] = await requests(fn)
    expect(post).toMatchObject({ path: '/auth/register', body: { accept_privacy: true } })
  })

  it('shows no checkbox and sends no consent field without a policy URL', async () => {
    const { fn } = mockFetch(() => json({ ok: true }, 202))
    const { wrapper } = await mountPage(RegisterPage, { path: '/register', me: null, config: cfg(null) })
    await settle()
    expect(wrapper.find('[data-testid="register-privacy"]').exists()).toBe(false)
    await wrapper.find('[data-testid="register-name"] input').setValue('Carla')
    await wrapper.find('[data-testid="register-email"] input').setValue('carla@example.com')
    await wrapper.find('[data-testid="register-password"] input').setValue('a-long-password-1')
    await wrapper.find('[data-testid="register-confirm"] input').setValue('a-long-password-1')
    await wrapper.find('[data-testid="register-form"]').trigger('submit')
    await settle(8)
    const [post] = await requests(fn)
    expect(post!.body).not.toHaveProperty('accept_privacy')
  })
})
