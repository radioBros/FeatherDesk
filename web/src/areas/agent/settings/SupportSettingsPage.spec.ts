// SPDX-License-Identifier: AGPL-3.0-only

import SupportSettingsPage from './SupportSettingsPage.vue'
import SupportSettingsForm from './SupportSettingsForm.vue'
import { mountAgent, fx, json, byTestId, typeInto, click } from '../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const SETTINGS = {
  tier_labels: { 1: 'Front desk', 2: 'Specialist', 3: 'Engineering' },
  auto_close_days: 7,
  reopen_window_days: 7,
  self_registration: false,
  customer_urgent_allowed: false,
  triage_visible_to_agents: true,
  kb_enabled: true,
  default_locale: 'en',
}

describe('SupportSettingsPage (FE-029)', () => {
  it('renders the loaded settings', async () => {
    const h = await mountAgent(SupportSettingsPage, { me: fx.me('admin'), api: { 'GET /admin/settings': SETTINGS } })
    await h.flush()
    expect((byTestId('tier-2')!.querySelector('input') as HTMLInputElement).value).toBe('Specialist')
    expect((byTestId('auto-close-days')!.querySelector('input') as HTMLInputElement).value).toBe('7')
  })

  it('rejects an out-of-range auto-close value before sending', async () => {
    const h = await mountAgent(SupportSettingsPage, { me: fx.me('admin'), api: { 'GET /admin/settings': SETTINGS, 'PATCH /admin/settings': SETTINGS } })
    await h.flush()
    await typeInto(byTestId('auto-close-days'), '400')
    await click(byTestId('settings-save'))
    await h.flush()
    expect(h.wrapper.text()).toContain('Enter a value between 1 and 365')
    expect(h.calls('PATCH /admin/settings')).toHaveLength(0)
    await typeInto(byTestId('auto-close-days'), '2.5')
    await click(byTestId('settings-save'))
    expect(h.calls('PATCH /admin/settings')).toHaveLength(0)
  })

  it('saves and refreshes the public config (tier labels)', async () => {
    const h = await mountAgent(SupportSettingsPage, {
      me: fx.me('admin'),
      config: { email_enabled: true },
      api: {
        'GET /admin/settings': SETTINGS,
        'PATCH /admin/settings': (_u, init) => json({ ...SETTINGS, ...init.body }),
        'GET /auth/config': { tier_labels: { 1: 'Desk', 2: 'Specialist', 3: 'Engineering' } },
      },
    })
    await h.flush()
    await typeInto(byTestId('tier-1'), 'Desk')
    await click(byTestId('settings-save'))
    await h.flush()
    const body = h.calls('PATCH /admin/settings')[0]!.body as Record<string, unknown>
    expect(body).toMatchObject({ tier_labels: { 1: 'Desk', 2: 'Specialist', 3: 'Engineering' }, auto_close_days: 7, self_registration: false })
    expect(h.calls('GET /auth/config')).toHaveLength(1)
  })

  it('disables self-registration while email is off and leaves it out of the body', async () => {
    const h = await mountAgent(SupportSettingsPage, {
      me: fx.me('admin'),
      config: { email_enabled: false },
      api: { 'GET /admin/settings': SETTINGS, 'PATCH /admin/settings': SETTINGS, 'GET /auth/config': {} },
    })
    await h.flush()
    expect(byTestId('self-registration')!.querySelector('input')!.hasAttribute('disabled')).toBe(true)
    expect(h.wrapper.text()).toContain('Needs working email')
    await click(byTestId('settings-save'))
    await h.flush()
    expect(h.calls('PATCH /admin/settings')[0]!.body).not.toHaveProperty('self_registration')
  })

  it('the customer portal switch (portal_enabled) warns when off and is sent on save', async () => {
    const h = await mountAgent(SupportSettingsPage, {
      me: fx.me('admin'),
      config: { email_enabled: true },
      api: { 'GET /admin/settings': { ...SETTINGS, portal_enabled: true }, 'PATCH /admin/settings': (_u, init) => json({ ...SETTINGS, ...init.body }), 'GET /auth/config': {} },
    })
    await h.flush()
    const input = byTestId('portal-enabled')!.querySelector('input') as HTMLInputElement
    expect(input.checked).toBe(true)
    expect(byTestId('portal-off-warning')).toBeFalsy()
    expect(h.wrapper.text()).toContain('only admins and staff sign in')
    await click(input)
    await h.flush()
    expect(byTestId('portal-off-warning')).toBeTruthy()
    await click(byTestId('settings-save'))
    await h.flush()
    expect(h.calls('PATCH /admin/settings')[0]!.body).toMatchObject({ portal_enabled: false, kb_enabled: true })
  })

  it('shows an error when loading fails', async () => {
    const h = await mountAgent(SupportSettingsPage, { me: fx.me('admin'), api: { 'GET /admin/settings': json({ title: 'x' }, 500) } })
    await h.flush()
    expect(byTestId('load-error')).toBeTruthy()
  })
})

describe('SupportSettingsForm (FE-005)', () => {
  it('an empty submit shows the required message and sends nothing', async () => {
    const h = await mountAgent(SupportSettingsForm, { props: { settings: null } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })
})
