// SPDX-License-Identifier: AGPL-3.0-only

import RegisterPage from './pages/RegisterPage.vue'
import ForgotPasswordPage from './pages/ForgotPasswordPage.vue'
import ResetPasswordPage from './pages/ResetPasswordPage.vue'
import VerifyEmailPage from './pages/VerifyEmailPage.vue'
import MagicLinkPage from './pages/MagicLinkPage.vue'
import InvitePage from './pages/InvitePage.vue'
import { mockFetch, json } from '@/test/mount'
import { mountPage, requests, settle } from './testing'

afterEach(() => vi.unstubAllGlobals())

const ME = { id: 'u1', kind: 'customer', role: 'requester', name: 'New', email: 'new@example.com', csrf_token: 'c' }

describe('register (FE-014)', () => {
  it('empty submit sends nothing; a valid one posts and shows the check-your-inbox state', async () => {
    const { fn } = mockFetch(() => json({ ok: true }, 202))
    const { wrapper } = await mountPage(RegisterPage, { path: '/register', me: null })
    await wrapper.find('[data-testid="register-form"]').trigger('submit')
    await settle()
    expect(fn).not.toHaveBeenCalled()
    await wrapper.find('[data-testid="register-name"] input').setValue('New Person')
    await wrapper.find('[data-testid="register-email"] input').setValue('new@example.com')
    await wrapper.find('[data-testid="register-password"] input').setValue('long-enough-pw')
    await wrapper.find('[data-testid="register-confirm"] input').setValue('different-pw!!')
    await wrapper.find('[data-testid="register-form"]').trigger('submit')
    await settle()
    expect(fn).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('The values do not match')
    await wrapper.find('[data-testid="register-confirm"] input').setValue('long-enough-pw')
    await wrapper.find('[data-testid="register-form"]').trigger('submit')
    await settle(8)
    const [req] = await requests(fn)
    expect(req).toMatchObject({ method: 'POST', path: '/auth/register', body: { name: 'New Person', email: 'new@example.com', password: 'long-enough-pw', locale: 'en' } })
    expect(wrapper.find('[data-testid="register-done"]').text()).toContain('new@example.com')
  })

  it('a 422 lands on the right field', async () => {
    mockFetch(() => json({ type: 'x/validation', title: 'Invalid', status: 422, errors: [{ field: 'email', code: 'email' }] }, 422))
    const { wrapper } = await mountPage(RegisterPage, { path: '/register', me: null })
    await wrapper.find('[data-testid="register-name"] input').setValue('N')
    await wrapper.find('[data-testid="register-email"] input').setValue('taken@example.com')
    await wrapper.find('[data-testid="register-password"] input').setValue('long-enough-pw')
    await wrapper.find('[data-testid="register-confirm"] input').setValue('long-enough-pw')
    await wrapper.find('[data-testid="register-form"]').trigger('submit')
    await settle(8)
    expect(wrapper.find('[data-testid="register-email"]').text()).toContain('Enter a valid email address')
  })
})

describe('forgot password (FE-014)', () => {
  it.each([['known@example.com'], ['unknown@example.com']])('shows the identical "if an account exists" message for %s', async (email) => {
    mockFetch(() => json({ ok: true }, 202))
    const { wrapper } = await mountPage(ForgotPasswordPage, { path: '/forgot-password', me: null, config: { email_enabled: true } })
    await wrapper.find('[data-testid="forgot-email"] input').setValue(email)
    await wrapper.find('[data-testid="forgot-form"]').trigger('submit')
    await settle(8)
    expect(wrapper.find('[data-testid="forgot-done"]').text()).toBe(
      'If an account exists for this address, we have sent a link to reset the password. Check your inbox.',
    )
  })
})

describe('reset password (FE-014)', () => {
  it('an expired token shows the problem detail and a "request new link" CTA', async () => {
    mockFetch(() => json({ type: 'x/bad-request', title: 'Bad request', status: 400, detail: 'This reset link has expired.' }, 400))
    const { wrapper } = await mountPage(ResetPasswordPage, { path: '/reset-password?token=abc', pattern: '/reset-password', me: null })
    await wrapper.find('[data-testid="reset-password"] input').setValue('a-new-password')
    await wrapper.find('[data-testid="reset-confirm"] input').setValue('a-new-password')
    await wrapper.find('[data-testid="reset-form"]').trigger('submit')
    await settle(8)
    const box = wrapper.find('[data-testid="reset-expired"]')
    expect(box.text()).toContain('This reset link has expired.')
    expect(wrapper.find('[data-testid="reset-new-link"]').attributes('href')).toBe('/forgot-password')
  })

  it('a valid reset posts the token and shows success', async () => {
    const { fn } = mockFetch(() => new Response(null, { status: 204 }))
    const { wrapper } = await mountPage(ResetPasswordPage, { path: '/reset-password?token=abc', pattern: '/reset-password', me: null })
    await wrapper.find('[data-testid="reset-password"] input').setValue('a-new-password')
    await wrapper.find('[data-testid="reset-confirm"] input').setValue('a-new-password')
    await wrapper.find('[data-testid="reset-form"]').trigger('submit')
    await settle(8)
    expect((await requests(fn))[0]).toMatchObject({ path: '/auth/password/reset', body: { token: 'abc', password: 'a-new-password' } })
    expect(wrapper.find('[data-testid="reset-done"]').exists()).toBe(true)
  })
})

describe('verify email (FE-014)', () => {
  it('auto-submits the token once and shows success', async () => {
    const { fn } = mockFetch(() => json(ME))
    const { wrapper, session } = await mountPage(VerifyEmailPage, { path: '/verify-email?token=tok', pattern: '/verify-email', me: null })
    await settle(8)
    const reqs = await requests(fn)
    expect(reqs).toHaveLength(1)
    expect(reqs[0]).toMatchObject({ method: 'POST', path: '/auth/verify-email', body: { token: 'tok' } })
    expect(wrapper.find('[data-testid="verify-done"]').exists()).toBe(true)
    expect(session.me?.email).toBe('new@example.com')
  })

  it('an expired token shows the failure state with a way back', async () => {
    mockFetch(() => json({ type: 'x/bad-request', title: 'Bad', status: 400, detail: 'Expired link' }, 400))
    const { wrapper } = await mountPage(VerifyEmailPage, { path: '/verify-email?token=old', pattern: '/verify-email', me: null })
    await settle(8)
    expect(wrapper.find('[data-testid="verify-failed"]').text()).toContain('Expired link')
  })
})

describe('magic link and invite landings (FE-034)', () => {
  it('/magic-link consumes the token and opens the tickets', async () => {
    const { fn } = mockFetch(() => json(ME))
    const { router } = await mountPage(MagicLinkPage, { path: '/magic-link?token=m1', pattern: '/magic-link', me: null })
    await settle(8)
    expect((await requests(fn))[0]).toMatchObject({ path: '/auth/magic-link', body: { token: 'm1' } })
    expect(router.currentRoute.value.path).toBe('/tickets')
  })

  it('/invite accepts with name + password and signs in', async () => {
    const { fn } = mockFetch(() => json(ME))
    const { wrapper, router, session } = await mountPage(InvitePage, { path: '/invite?token=i1', pattern: '/invite', me: null })
    await wrapper.find('[data-testid="invite-form"]').trigger('submit')
    await settle()
    expect(fn).not.toHaveBeenCalled()
    await wrapper.find('[data-testid="invite-name"] input').setValue('Ivy')
    await wrapper.find('[data-testid="invite-password"] input').setValue('ivy-password-1')
    await wrapper.find('[data-testid="invite-confirm"] input').setValue('ivy-password-1')
    await wrapper.find('[data-testid="invite-form"]').trigger('submit')
    await settle(8)
    expect((await requests(fn))[0]).toMatchObject({ path: '/auth/invite/accept', body: { token: 'i1', name: 'Ivy', password: 'ivy-password-1' } })
    expect(session.me?.id).toBe('u1')
    expect(router.currentRoute.value.path).toBe('/tickets')
  })
})
