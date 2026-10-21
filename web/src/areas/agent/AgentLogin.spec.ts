// SPDX-License-Identifier: AGPL-3.0-only

import AgentLoginPage from './AgentLoginPage.vue'
import NoAccessPage from './NoAccessPage.vue'
import { mountAgent, fx } from './testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('AgentLoginPage (FE-020)', () => {
  it('with SSO only, no password field until the break-glass disclosure is opened', async () => {
    const h = await mountAgent(AgentLoginPage, { me: null, config: { staff: { oidc: true, local: false } } })
    expect(h.wrapper.find('[data-testid="sso-button"]').exists()).toBe(true)
    expect(h.wrapper.find('input[type="password"]').exists()).toBe(false)
    await h.wrapper.find('[data-testid="break-glass-toggle"]').trigger('click')
    await h.flush()
    expect(h.wrapper.find('input[type="password"]').exists()).toBe(true)
  })

  it('shows the local form directly when staff local login is on', async () => {
    const h = await mountAgent(AgentLoginPage, { me: null, config: { staff: { oidc: false, local: true } } })
    expect(h.wrapper.find('[data-testid="sso-button"]').exists()).toBe(false)
    expect(h.wrapper.find('input[type="password"]').exists()).toBe(true)
  })

  it('?error=no_role shows the translated reason; unknown codes a generic one', async () => {
    const h = await mountAgent(AgentLoginPage, { me: null, route: '/agent/login?error=no_role', config: { staff: { oidc: true, local: false } } })
    expect(h.wrapper.find('[data-testid="login-error"]').text()).toContain('no support role')
    h.wrapper.unmount()
    const g = await mountAgent(AgentLoginPage, { me: null, route: '/agent/login?error=weird', config: { staff: { oidc: true, local: false } } })
    expect(g.wrapper.find('[data-testid="login-error"]').text()).toContain('Sign-in failed')
  })

  it('the SSO button keeps a same-origin redirect only', async () => {
    const h = await mountAgent(AgentLoginPage, { me: null, route: '/agent/login?redirect=/agent/tickets/7', config: { staff: { oidc: true, local: false } } })
    expect(h.wrapper.find('[data-testid="sso-button"]').attributes('href')).toBe('/api/v1/auth/oidc/staff/start?redirect=%2Fagent%2Ftickets%2F7')
  })
})

describe('NoAccessPage (FE-044)', () => {
  it('explains the missing role and offers sign-out and the portal', async () => {
    const h = await mountAgent(NoAccessPage, { me: fx.me('agent', 1) })
    expect(h.wrapper.text()).toContain('No access to the agent panel')
    expect(h.wrapper.find('[data-testid="no-access-signout"]').exists()).toBe(true)
    expect(h.wrapper.find('[data-testid="no-access-portal"]').exists()).toBe(true)
  })
})
