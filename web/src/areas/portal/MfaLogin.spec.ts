// SPDX-License-Identifier: AGPL-3.0-only

import PortalLoginPage from './PortalLoginPage.vue'
import MagicLinkPage from './pages/MagicLinkPage.vue'
import VerifyEmailPage from './pages/VerifyEmailPage.vue'
import InvitePage from './pages/InvitePage.vue'
import AgentLoginPage from '../agent/AgentLoginPage.vue'
import { mockFetch, json } from '@/test/mount'
import { mountAgent } from '../agent/testing'
import { mountPage, requests, settle } from './testing'

/** FE-070: every first factor that answers `MfaChallenge` continues in the MFA step, never straight into the app. */
afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const CH = { mfa_required: true, mfa_token: 'tok-9', methods: ['totp', 'recovery'], enrollment_required: false, expires_at: '2026-10-21T10:05:00Z' }
const ME = { id: 'u', kind: 'customer', role: 'requester', name: 'C', email: 'c@x.io', csrf_token: 't' }

function route(url: string) {
  if (url.includes('/auth/mfa/verify')) return json(ME)
  if (url.includes('/auth/')) return json(CH)
  return json(ME)
}

describe('MFA step after the first factor (FE-070)', () => {
  it('portal: a correct password shows the code step; the code signs in and redirects', async () => {
    const { fn } = mockFetch(route)
    const { wrapper, router } = await mountPage(PortalLoginPage, { path: '/login?redirect=/tickets/42', pattern: '/login', me: null })
    await wrapper.find('[data-testid="login-email"] input').setValue('c@x.io')
    await wrapper.find('[data-testid="login-password"] input').setValue('secret-password')
    await wrapper.find('[data-testid="login-form"]').trigger('submit')
    await settle(8)
    expect(wrapper.find('[data-testid="mfa-step"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="login-form"]').exists()).toBe(false)
    expect(router.currentRoute.value.path).toBe('/login')
    await wrapper.find('[data-testid="mfa-code"] input').setValue('123456')
    await wrapper.find('[data-testid="mfa-code-form"]').trigger('submit')
    await settle(8)
    const reqs = await requests(fn)
    expect(reqs.find((r) => r.path === '/auth/mfa/verify')).toMatchObject({ body: { mfa_token: 'tok-9', method: 'totp', code: '123456' } })
    expect(reqs.some((r) => r.path === '/me')).toBe(false)
    expect(router.currentRoute.value.fullPath).toBe('/tickets/42')
  })

  it('agent: the staff login page continues with the code step', async () => {
    const h = await mountAgent(AgentLoginPage, { me: null, config: { staff: { oidc: false, local: true } }, api: { 'POST /auth/login': CH } })
    await h.wrapper.find('[data-testid="login-email"] input').setValue('a@x.io')
    await h.wrapper.find('[data-testid="login-password"] input').setValue('secret-password')
    await h.wrapper.find('[data-testid="login-form"]').trigger('submit')
    await h.flush()
    await h.flush()
    expect(h.wrapper.find('[data-testid="mfa-step"]').exists()).toBe(true)
    await h.wrapper.find('[data-testid="mfa-cancel"]').trigger('click')
    await h.flush()
    expect(h.wrapper.find('[data-testid="login-form"]').exists()).toBe(true)
  })

  it.each([
    ['magic link', MagicLinkPage, '/magic-link?token=m1'],
    ['verify email', VerifyEmailPage, '/verify-email?token=v1'],
  ])('%s: a challenge is shown instead of signing in', async (_n, page, path) => {
    mockFetch(route)
    const { wrapper } = await mountPage(page, { path, pattern: path.split('?')[0], me: null })
    await settle(8)
    expect(wrapper.find('[data-testid="mfa-step"]').exists()).toBe(true)
  })

  it('invitation: the policy-forced challenge follows the accept form', async () => {
    mockFetch(route)
    const { wrapper } = await mountPage(InvitePage, { path: '/invite?token=i1', pattern: '/invite', me: null })
    await wrapper.find('[data-testid="invite-name"] input').setValue('Ivy')
    await wrapper.find('[data-testid="invite-password"] input').setValue('ivy-password-1')
    await wrapper.find('[data-testid="invite-confirm"] input').setValue('ivy-password-1')
    await wrapper.find('[data-testid="invite-form"]').trigger('submit')
    await settle(8)
    expect(wrapper.find('[data-testid="mfa-step"]').exists()).toBe(true)
  })
})
