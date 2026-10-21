// SPDX-License-Identifier: AGPL-3.0-only

import PortalLoginPage from './PortalLoginPage.vue'
import { mockFetch, json } from '@/test/mount'
import { mountPage, requests, settle } from './testing'

afterEach(() => vi.unstubAllGlobals())

const page = (config = {}) => mountPage(PortalLoginPage, { path: '/login', me: null, config })

describe('portal login (FE-013)', () => {
  it('an empty submit makes no API call and shows the required messages', async () => {
    const { fn } = mockFetch(() => json({}))
    const { wrapper } = await page({ email_enabled: true })
    await wrapper.find('[data-testid="login-form"]').trigger('submit')
    await settle()
    expect(fn).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('This field is required')
  })

  it('a 401 shows a generic error without account enumeration', async () => {
    mockFetch(() => json({ type: 'https://simpletickets.dev/problems/unauthenticated', title: 'No such user', status: 401 }, 401))
    const { wrapper } = await page()
    await wrapper.find('[data-testid="login-email"] input').setValue('nobody@example.com')
    await wrapper.find('[data-testid="login-password"] input').setValue('wrong-password')
    await wrapper.find('[data-testid="login-form"]').trigger('submit')
    await settle(8)
    const err = wrapper.find('[data-testid="form-error"]')
    expect(err.text()).toBe('Email or password is not correct.')
    expect(wrapper.text()).not.toContain('No such user')
  })

  it('posts the customer audience and goes to the redirect target', async () => {
    const { fn } = mockFetch((url) => (url.endsWith('/me') ? json({ id: 'u', kind: 'customer', role: 'requester', name: 'C', email: 'c@x.io', csrf_token: 't' }) : json({})))
    const { wrapper, router } = await mountPage(PortalLoginPage, { path: '/login?redirect=/tickets/42', pattern: '/login', me: null })
    await wrapper.find('[data-testid="login-email"] input').setValue('c@x.io')
    await wrapper.find('[data-testid="login-password"] input').setValue('secret-password')
    await wrapper.find('[data-testid="login-form"]').trigger('submit')
    await settle(8)
    const reqs = await requests(fn)
    expect(reqs[0]).toMatchObject({ method: 'POST', path: '/auth/login', body: { audience: 'customer', email: 'c@x.io' } })
    expect(router.currentRoute.value.fullPath).toBe('/tickets/42')
  })

  it('hides the Register link when self-registration is disabled', async () => {
    mockFetch()
    const on = await page({ email_enabled: true, customer: { local: true, oidc: false, self_registration: true } })
    expect(on.wrapper.find('[data-testid="register-link"]').exists()).toBe(true)
    on.wrapper.unmount()
    const off = await page({ email_enabled: true, customer: { local: true, oidc: false, self_registration: false } })
    expect(off.wrapper.find('[data-testid="register-link"]').exists()).toBe(false)
    expect(off.wrapper.find('[data-testid="forgot-link"]').exists()).toBe(true)
  })

  it('shows the customer OIDC button only when the customer audience lists oidc (FE-034)', async () => {
    mockFetch()
    const off = await page()
    expect(off.wrapper.find('[data-testid="sso-button"]').exists()).toBe(false)
    off.wrapper.unmount()
    const on = await page({ customer: { local: true, oidc: true, oidc_label: 'Sign in with ACME' } })
    const btn = on.wrapper.find('[data-testid="sso-button"]')
    expect(btn.text()).toBe('Sign in with ACME')
    expect(btn.attributes('href')).toBe('/api/v1/auth/oidc/customer/start')
  })

  it('offers the magic link only when enabled and email works (FE-034)', async () => {
    const { fn } = mockFetch(() => json({ ok: true }))
    const off = await page({ email_enabled: false, customer: { local: true, oidc: false, magic_link: true } })
    expect(off.wrapper.find('[data-testid="magic-toggle"]').exists()).toBe(false)
    off.wrapper.unmount()
    const { wrapper } = await page({ email_enabled: true, customer: { local: true, oidc: false, magic_link: true } })
    await wrapper.find('[data-testid="magic-toggle"]').trigger('click')
    await settle()
    await wrapper.find('[data-testid="magic-form"]').trigger('submit')
    await settle()
    expect(fn).not.toHaveBeenCalled()
    await wrapper.find('[data-testid="magic-email"] input').setValue('c@x.io')
    await wrapper.find('[data-testid="magic-form"]').trigger('submit')
    await settle(8)
    expect((await requests(fn))[0]).toMatchObject({ method: 'POST', path: '/auth/magic-link', body: { email: 'c@x.io' } })
    expect(wrapper.find('[data-testid="magic-sent"]').exists()).toBe(true)
  })
})
