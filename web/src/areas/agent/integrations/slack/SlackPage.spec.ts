// SPDX-License-Identifier: AGPL-3.0-only

import SlackPage from './SlackPage.vue'
import SlackRouteForm from './SlackRouteForm.vue'
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
import { mountAgent, fx, json, byTestId } from '../../testing'
import { slackRoute, slackStatus } from './fixtures'
import { presetEvents } from './triggers'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

const connected = slackStatus({
  state: 'connected', client_id: '123.456', client_secret_set: true, team_name: 'Acme', team_url: 'https://acme.slack.com/',
  bot_name: 'acmehelp', scopes: ['chat:write', 'channels:read'], connected_at: '2026-10-23T09:00:00Z', connected_by: { id: 'u1', name: 'Ada' },
})

describe('SlackPage (D-21, INT-013/014)', () => {
  it('walks the setup: manifest link, credentials, then connect through the popup', async () => {
    let current = slackStatus({ https: false, redirect_url: 'http://localhost:8080/api/v1/integrations/slack/oauth/callback' })
    const h = await mountAgent(SlackPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/integrations/slack': () => json(current),
        'PATCH /admin/integrations/slack': (_u, init) => json((current = { ...current, state: 'configured', client_id: init.body.client_id, client_secret_set: true })),
        'POST /admin/integrations/slack/oauth/start': { authorize_url: 'https://slack.test/authorize', state: 's1', expires_at: '2026-10-23T10:10:00Z' },
      },
    })
    expect(byTestId('slack-setup')).not.toBeNull()
    expect(byTestId('slack-https-warning')!.textContent).toContain('http://localhost:8080')
    expect(byTestId('slack-create-app')!.getAttribute('href')).toContain('manifest_json=')
    expect(byTestId('slack-connect')!.hasAttribute('disabled')).toBe(true)

    await h.wrapper.find('[data-testid="slack-client-id"] input').setValue('123.456')
    await h.wrapper.find('[data-testid="slack-client-secret"] input').setValue('s3cret')
    await h.wrapper.find('[data-testid="slack-app-form"]').trigger('submit')
    await h.flush()
    expect(h.calls('PATCH /admin/integrations/slack')[0]!.body).toEqual({ client_id: '123.456', client_secret: 's3cret' })
    expect(byTestId('slack-connect')!.hasAttribute('disabled')).toBe(false)

    // Connect opens a popup (never this tab) and waits for it.
    const popup = { location: { href: '' }, closed: false, focus: vi.fn(), close: vi.fn() }
    const open = vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    byTestId('slack-connect')!.click()
    await h.flush()
    expect(open).toHaveBeenCalledTimes(1)
    expect(popup.location.href).toBe('https://slack.test/authorize')
    expect(byTestId('oauth-waiting')).not.toBeNull()
    expect(byTestId('slack-connect')!.hasAttribute('disabled')).toBe(true)
    byTestId('oauth-waiting-cancel')!.click()
    await h.flush()
    expect(popup.close).toHaveBeenCalled()
    expect(byTestId('slack-connect')!.hasAttribute('disabled')).toBe(false)
  })

  it('asks to allow popups when the browser blocks one', async () => {
    const h = await mountAgent(SlackPage, {
      me: fx.me('admin'),
      api: { 'GET /admin/integrations/slack': slackStatus({ state: 'configured', client_id: '1.2', client_secret_set: true }) },
    })
    vi.spyOn(window, 'open').mockReturnValue(null)
    const href = window.location.href
    byTestId('slack-connect')!.click()
    await h.flush()
    expect(byTestId('oauth-blocked')!.textContent).toContain('Allow popups')
    expect(window.location.href).toBe(href)
    expect(h.calls('POST /admin/integrations/slack/oauth/start')).toHaveLength(0)
  })

  it('shows the workspace, the rules and their channel errors', async () => {
    const h = await mountAgent(SlackPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/integrations/slack': connected,
        'GET /admin/integrations/slack/routes': [slackRoute(), slackRoute({ id: 'r2', channel_name: 'alerts', channel_private: true, last_error: 'not_in_channel', active: false })],
      },
    })
    await h.flush()
    expect(byTestId('slack-team')!.textContent).toContain('Acme')
    const cards = document.querySelectorAll('[data-testid="slack-route-card"]')
    expect(cards).toHaveLength(2)
    expect(cards[0]!.textContent).toContain('New ticket')
    expect(cards[1]!.querySelector('[data-testid="slack-route-error"]')!.textContent).toContain('/invite @acmehelp')
    expect(cards[1]!.textContent).toContain('Paused')
  })

  it('offers reconnecting when Slack revoked the token', async () => {
    await mountAgent(SlackPage, {
      me: fx.me('admin'),
      api: { 'GET /admin/integrations/slack': { ...connected, state: 'error', error: 'token_revoked' }, 'GET /admin/integrations/slack/routes': [] },
    })
    expect(byTestId('slack-broken')!.textContent).toContain('token_revoked')
    expect(byTestId('slack-reconnect-broken')).not.toBeNull()
    expect(byTestId('slack-rule-quickstart')).not.toBeNull()
  })
})

describe('SlackRouteForm (D-21, INT-014)', () => {
  const channels = [
    { id: 'C0SUPPORT', name: 'support', is_private: false, is_member: false, num_members: 12 },
    { id: 'G0ALERTS', name: 'alerts', is_private: true, is_member: false, num_members: 3 },
  ]

  it('applies presets, previews the posts and sends the rule', async () => {
    const h = await mountAgent(SlackRouteForm, {
      me: fx.me('admin'),
      props: { channels, preset: 'newTickets' },
      api: { 'POST /admin/integrations/slack/routes': (_u, init) => json(slackRoute(init.body), 201) },
    })
    expect(byTestId('slack-preview')!.textContent).toContain('New ticket from')
    expect(document.querySelectorAll('[data-testid="slack-preview-reply"]')).toHaveLength(0)

    byTestId('slack-preset-updates')!.click()
    await h.flush()
    expect(document.querySelectorAll('[data-testid="slack-preview-reply"]')).toHaveLength(3) // first post + 3 thread replies at most
    expect(byTestId('slack-preview')!.textContent).toContain('3 replies')

    h.wrapper.findComponent(AutocompleteSearch).vm.$emit('update:modelValue', 'G0ALERTS')
    await h.flush()
    await h.wrapper.find('[data-testid="slack-route-form"]').trigger('submit')
    await h.flush()
    const body = h.calls('POST /admin/integrations/slack/routes')[0]!.body as Record<string, unknown>
    expect(body).toMatchObject({ channel_id: 'G0ALERTS', channel_name: 'alerts', channel_private: true, events: presetEvents('updates', false), threads: true })
  })

  it('keeps internal notes out unless the rule includes internal details', () => {
    expect(presetEvents('everything', false)).not.toContain('note.created')
    expect(presetEvents('everything', true)).toContain('note.created')
  })
})
