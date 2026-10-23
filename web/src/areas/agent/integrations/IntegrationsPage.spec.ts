// SPDX-License-Identifier: AGPL-3.0-only

import IntegrationsPage from './IntegrationsPage.vue'
import { mountAgent, fx, byTestId } from '../testing'
import { slackStatus } from './slack/fixtures'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('IntegrationsPage (D-21, INT-012)', () => {
  it('shows every integration with its live status', async () => {
    await mountAgent(IntegrationsPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/integrations/slack': slackStatus({ state: 'connected', team_name: 'Acme', active_routes: 2 }),
        'GET /admin/integrations': { mcp_enabled: true },
        'GET /admin/webhooks': [{ id: 'w1', active: true }, { id: 'w2', active: false }],
      },
    })
    const slack = byTestId('integration-slack')!
    expect(slack.textContent).toContain('Slack')
    expect(slack.textContent).toContain('Connected')
    expect(slack.textContent).toContain('Acme · 2 active rules')
    expect(byTestId('integration-ai')!.textContent).toContain('On')
    expect(byTestId('integration-webhooks')!.textContent).toContain('1 active')
    expect(byTestId('integration-api')!.getAttribute('href')).toBe('/api/docs')
  })

  it('flags a broken Slack token and keeps the other cards when one status fails', async () => {
    await mountAgent(IntegrationsPage, {
      me: fx.me('admin'),
      api: { 'GET /admin/integrations/slack': slackStatus({ state: 'error', error: 'token_revoked' }), 'GET /admin/webhooks': [] },
    })
    expect(byTestId('integration-slack')!.textContent).toContain('Needs attention')
    expect(byTestId('integration-slack')!.textContent).toContain('token_revoked')
    expect(byTestId('integration-ai')!.querySelector('[data-testid="integration-status"]')).toBeNull()
    expect(byTestId('integration-webhooks')!.textContent).toContain('None active')
  })
})
