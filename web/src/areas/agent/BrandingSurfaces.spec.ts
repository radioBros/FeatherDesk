// SPDX-License-Identifier: AGPL-3.0-only

/** FE-050 (agent half) and FE-037 (agent half): login branding, admin email banner, revoking the current session. */
import AgentLayout from './AgentLayout.vue'
import AgentLoginPage from './AgentLoginPage.vue'
import ProfilePage from './directory/ProfilePage.vue'
import { mountAgent, fx, byTestId } from './testing'
import { settleConfirm } from '@/composables/useConfirm'
import { useSessionStore } from '@/stores/session'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const api = { 'GET /agent/tickets/counts': { mine: 0, unassigned: 0, triage: 0, tier: {} }, 'GET /agent/views': [] }

describe('agent branding surfaces (FE-050)', () => {
  it('the "email not configured" banner shows for an admin and not for an agent', async () => {
    const admin = await mountAgent(AgentLayout, { me: fx.me('admin'), api, config: { email_enabled: false } })
    expect(byTestId('email-off-banner')?.textContent).toContain('Email is not configured')
    admin.wrapper.unmount()
    document.body.innerHTML = ''
    await mountAgent(AgentLayout, { me: fx.me('agent'), api, config: { email_enabled: false } })
    expect(byTestId('email-off-banner')).toBeNull()
    document.body.innerHTML = ''
    await mountAgent(AgentLayout, { me: fx.me('admin'), api, config: { email_enabled: true } })
    expect(byTestId('email-off-banner')).toBeNull()
  })

  it('the agent login uses the branded title, subtitle and background colour', async () => {
    await mountAgent(AgentLoginPage, {
      me: null,
      path: '/agent/login',
      config: { branding: { product_name: 'Acme', primary_color: '#123456', default_theme: 'light', radius: 'default', powered_by: true, login_title: 'Acme Support Desk', login_subtitle: 'Staff only', login_background_color: '#eeeeee' } },
    })
    expect(byTestId('agent-login-title')?.textContent).toContain('Acme Support Desk')
    expect(document.body.textContent).toContain('Staff only')
    expect(byTestId('agent-login-main')?.getAttribute('style')).toContain('background-color')
  })
})

describe('agent sessions (FE-037, agent half)', () => {
  it('revoking the current session signs this tab out', async () => {
    const h = await mountAgent(ProfilePage, {
      routes: [{ path: '/agent/login' }],
      api: {
        'GET /me/signature': { signature_md: '' },
        'GET /me/sessions': [{ id: 's-1', audience: 'staff', created_at: fx.iso, expires_at: fx.iso, current: true, user_agent: 'Firefox' }],
        'DELETE /me/sessions/s-1': () => new Response(null, { status: 204 }),
      },
    })
    await h.flush()
    const btn = h.wrapper.find('[data-testid="session-revoke"]')
    await btn.trigger('click')
    settleConfirm(true)
    await h.flush()
    expect(h.calls('DELETE /me/sessions/s-1')).toHaveLength(1)
    expect(useSessionStore().me).toBeNull()
    expect(h.router.currentRoute.value.path).toBe('/agent/login')
  })
})
