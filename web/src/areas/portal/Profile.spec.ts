// SPDX-License-Identifier: AGPL-3.0-only

import ProfilePage from './pages/ProfilePage.vue'
import { mockFetch, json } from '@/test/mount'
import { i18n, setLocale } from '@/i18n'
import { mountPage, requests, settle, CUSTOMER } from './testing'

afterEach(async () => {
  vi.unstubAllGlobals()
  await setLocale('en')
})

describe('portal profile (FE-018)', () => {
  it('passes FE-005: clearing the name blocks the save', async () => {
    const { fn } = mockFetch(() => json({}))
    const { wrapper } = await mountPage(ProfilePage, { path: '/profile' })
    await wrapper.find('[data-testid="profile-name"] input').setValue('')
    await wrapper.find('[data-testid="profile-form"]').trigger('submit')
    await settle()
    expect(fn).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('This field is required')
  })

  it('changing the locale to IT re-renders in Italian without a reload', async () => {
    const { fn } = mockFetch(() => json({ ...CUSTOMER, locale: 'it' }))
    const { wrapper } = await mountPage(ProfilePage, { path: '/profile' })
    ;(wrapper.findComponent({ name: 'ProfileForm' }).vm as unknown as { $: { setupState: { model: { locale: string } } } }).$.setupState.model.locale = 'it'
    await settle()
    await wrapper.find('[data-testid="profile-form"]').trigger('submit')
    // The locale switch lazy-loads it.json and the area packs (real dynamic imports): wait for it
    // instead of counting ticks, which is too short when the worker is busy.
    await vi.waitFor(() => expect(i18n.global.locale.value).toBe('it'), { timeout: 5000 })
    const [patch] = await requests(fn)
    // Email opt-outs moved to the notification matrix (FE-095): this form sends name and locale only.
    expect(patch).toMatchObject({ method: 'PATCH', path: '/me', body: { name: 'Carla Customer', locale: 'it' } })
    expect(patch!.body).not.toHaveProperty('notification_opt_out')
    expect(i18n.global.locale.value).toBe('it')
    expect(wrapper.find('[data-testid="profile-submit"]').text()).toBe('Salva')
  })

  it('the password section is absent when me.has_password=false', async () => {
    mockFetch()
    const withPw = await mountPage(ProfilePage, { path: '/profile' })
    expect(withPw.wrapper.find('[data-testid="password-section"]').exists()).toBe(true)
    withPw.wrapper.unmount()
    const oidc = await mountPage(ProfilePage, { path: '/profile', me: { ...CUSTOMER, has_password: false } })
    expect(oidc.wrapper.find('[data-testid="password-section"]').exists()).toBe(false)
  })

  it('the password form requires the confirmation to match before sending', async () => {
    const { fn } = mockFetch(() => json(CUSTOMER))
    const { wrapper } = await mountPage(ProfilePage, { path: '/profile' })
    await wrapper.find('[data-testid="pw-current"] input').setValue('old-password-1')
    await wrapper.find('[data-testid="pw-new"] input').setValue('new-password-12')
    await wrapper.find('[data-testid="pw-confirm"] input').setValue('new-password-13')
    await wrapper.find('[data-testid="password-form"]').trigger('submit')
    await settle()
    expect(fn).not.toHaveBeenCalled()
    await wrapper.find('[data-testid="pw-confirm"] input').setValue('new-password-12')
    await wrapper.find('[data-testid="password-form"]').trigger('submit')
    await vi.waitFor(async () => expect(await requests(fn)).not.toHaveLength(0))
    expect((await requests(fn))[0]).toMatchObject({ method: 'PATCH', path: '/me', body: { current_password: 'old-password-1', new_password: 'new-password-12' } })
  })
})
