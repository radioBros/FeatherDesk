// SPDX-License-Identifier: AGPL-3.0-only

import { createMemoryHistory, createRouter } from 'vue-router'
import SetupPage from './SetupPage.vue'
import { STORAGE_KEY } from './state'
import { decide } from '@/router/guards'
import { setupRoutes } from '@/router/routes'
import { mockFetch, json } from '@/test/mount'
import { mountPage, requests, resolveFor, settle } from '../portal/testing'

const TOKEN = 'abcdefghijklmnopqrstuvwxyz012345'
const PASSWORD = 'super-secret-admin-pw'
const ADMIN_ME = { id: 'a1', kind: 'staff', role: 'admin', name: 'Ada', email: 'ada@example.com', csrf_token: 'c2', tier: 3 }

// The later steps are lazy components (FE-031); load them once up front.
beforeAll(() => Promise.all([import('./forms/SetupAdminForm.vue'), import('./forms/SetupBrandingForm.vue'), import('./forms/SetupSmtpForm.vue')]))
beforeEach(() => sessionStorage.clear())
afterEach(() => vi.unstubAllGlobals())

const open = () => mountPage(SetupPage, { path: '/setup', me: null, config: { setup_pending: true } })
type W = Awaited<ReturnType<typeof open>>['wrapper']
const step = (w: W) => (w.findComponent({ name: 'VStepper' }).props('modelValue') as number)
const next = async (w: W, form: string) => {
  await w.find(`[data-testid="${form}"]`).trigger('submit')
  await settle(8)
}

async function toBranding(w: W) {
  await w.find('[data-testid="setup-token"] input').setValue(TOKEN)
  await next(w, 'setup-token-form')
  await w.find('[data-testid="setup-admin-name"] input').setValue('Ada')
  await w.find('[data-testid="setup-admin-email"] input').setValue('ada@example.com')
  await w.find('[data-testid="setup-admin-password"] input').setValue(PASSWORD)
  await w.find('[data-testid="setup-admin-confirm"] input').setValue(PASSWORD)
  await next(w, 'setup-admin-form')
}

describe('setup wizard (FE-049)', () => {
  it('Next on an empty step sends no request and stays on the step', async () => {
    const { fn } = mockFetch(() => json({}))
    const { wrapper } = await open()
    await next(wrapper, 'setup-token-form')
    expect(fn).not.toHaveBeenCalled()
    expect(step(wrapper)).toBe(1)
    expect(wrapper.text()).toContain('This field is required')
    await wrapper.find('[data-testid="setup-token"] input').setValue(TOKEN)
    await next(wrapper, 'setup-token-form')
    expect(step(wrapper)).toBe(2)
    await next(wrapper, 'setup-admin-form')
    expect(step(wrapper)).toBe(2)
    expect(fn).not.toHaveBeenCalled()
  })

  it('a wrong token shows the problem message back on step 1', async () => {
    mockFetch(() => json({ type: 'x/forbidden', title: 'Forbidden', status: 403, detail: 'The setup token is wrong.' }, 403))
    const { wrapper } = await open()
    await toBranding(wrapper)
    expect(step(wrapper)).toBe(3)
    await next(wrapper, 'setup-branding-form')
    expect(step(wrapper)).toBe(1)
    expect(wrapper.find('[data-testid="setup-token-error"]').text()).toBe('The setup token is wrong.')
  })

  it('the password is never written to sessionStorage', async () => {
    const writes: string[] = []
    const orig = Storage.prototype.setItem
    const spy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, k: string, v: string) {
      writes.push(`${k}=${v}`)
      return orig.call(this, k, v)
    })
    mockFetch(() => json(ADMIN_ME, 201))
    const { wrapper } = await open()
    await toBranding(wrapper)
    await next(wrapper, 'setup-branding-form')
    spy.mockRestore()
    expect(writes.length).toBeGreaterThan(0)
    expect(writes.some((w) => w.includes(PASSWORD))).toBe(false)
    expect(sessionStorage.getItem(STORAGE_KEY)).not.toContain(PASSWORD)
  })

  it('completes setup, signs the admin in, then SMTP Test validates first and calls the admin endpoint', async () => {
    const { fn } = mockFetch((url) => {
      if (url.includes('/setup/complete')) return json(ADMIN_ME, 201)
      if (url.includes('/admin/email/test')) return json({ ok: true })
      return json({})
    })
    const { wrapper, session } = await open()
    await toBranding(wrapper)
    await next(wrapper, 'setup-branding-form')
    const complete = (await requests(fn)).find((r) => r.path === '/setup/complete')!
    expect(complete.body).toMatchObject({
      setup_token: TOKEN,
      admin: { name: 'Ada', email: 'ada@example.com', password: PASSWORD },
      branding: { product_name: 'FeatherDesk', primary_color: '#1A73E8' },
    })
    expect(session.me?.id).toBe('a1')
    expect(step(wrapper)).toBe(4)

    // Test with an empty host: validate() stops it, nothing is sent.
    const before = fn.mock.calls.length
    await wrapper.find('[data-testid="smtp-test"]').trigger('click')
    await settle(6)
    expect(fn.mock.calls.length).toBe(before)
    expect(wrapper.find('[data-testid="setup-smtp-form"]').text()).toContain('This field is required')

    await wrapper.find('[data-testid="smtp-host"] input').setValue('smtp.example.com')
    await wrapper.find('[data-testid="smtp-from"] input').setValue('help@example.com')
    await wrapper.find('[data-testid="smtp-test"]').trigger('click')
    await settle(8)
    const test = (await requests(fn)).find((r) => r.path === '/admin/email/test')!
    expect(test.body).toMatchObject({ to: 'ada@example.com', config: { smtp: { host: 'smtp.example.com', port: 587, security: 'starttls' } } })
    expect(wrapper.find('[data-testid="smtp-test-result"]').text()).toContain('Test email sent')

    await wrapper.find('[data-testid="smtp-skip"]').trigger('click')
    await settle()
    expect(step(wrapper)).toBe(5)
    expect(wrapper.find('[data-testid="setup-sso"]').text()).toContain('/api/v1/auth/oidc/staff/callback')
  })

  it('with setup complete, /setup redirects away', () => {
    const router = createRouter({ history: createMemoryHistory(), routes: setupRoutes })
    const to = resolveFor(router, '/setup')
    expect(decide(to, { me: null, setupPending: false })).toEqual({ path: '/login' })
    expect(decide(to, { me: null, setupPending: true })).toBe(true)
  })
})
