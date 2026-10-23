// SPDX-License-Identifier: AGPL-3.0-only

import { startOAuthPopup, OAUTH_CHANNEL, type OAuthMessage } from './oauthPopup'
import OAuthCompletePage from './OAuthCompletePage.vue'
import { mountAgent, fx, byTestId } from '../../testing'

/** In-process BroadcastChannel: every other open instance with the same name receives the message. */
class FakeChannel {
  static open: FakeChannel[] = []
  onmessage: ((ev: MessageEvent) => void) | null = null
  constructor(public name: string) {
    FakeChannel.open.push(this)
  }
  postMessage(data: unknown) {
    for (const c of FakeChannel.open) if (c !== this && c.name === this.name) c.onmessage?.({ data } as MessageEvent)
  }
  close() {
    FakeChannel.open = FakeChannel.open.filter((c) => c !== this)
  }
}

function fakePopup() {
  return { location: { href: '' }, closed: false, focus: vi.fn(), close: vi.fn() }
}

beforeEach(() => {
  FakeChannel.open = []
  vi.stubGlobal('BroadcastChannel', FakeChannel)
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

const tick = () => new Promise((r) => setTimeout(r, 0))

describe('startOAuthPopup (D-21)', () => {
  it('opens the popup before the API call, then points it at the consent URL', async () => {
    const popup = fakePopup()
    const open = vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    let started = false
    const flow = startOAuthPopup('slack', async () => {
      started = true
      return { authorize_url: 'https://slack.test/oauth?state=s1', state: 's1' }
    })
    expect(open).toHaveBeenCalledWith('', 'fd-oauth-slack', expect.stringContaining('popup'))
    expect(started).toBe(true) // called synchronously after window.open
    await tick()
    expect(popup.location.href).toBe('https://slack.test/oauth?state=s1')

    // A result for another flow is ignored; ours resolves and is acknowledged.
    const page = new FakeChannel(OAUTH_CHANNEL)
    const acks: OAuthMessage[] = []
    page.onmessage = (ev) => acks.push(ev.data as OAuthMessage)
    page.postMessage({ type: 'result', state: 'other', result: 'ok' } satisfies OAuthMessage)
    page.postMessage({ type: 'result', provider: 'slack', state: 's1', result: 'error', reason: 'access_denied' } satisfies OAuthMessage)
    await expect(flow.promise).resolves.toEqual({ result: 'error', reason: 'access_denied' })
    expect(acks).toEqual([{ type: 'ack', state: 's1' }])
  })

  it('also accepts the result by window.postMessage from its own origin only', async () => {
    const popup = fakePopup()
    vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    const flow = startOAuthPopup('slack', async () => ({ authorize_url: 'https://slack.test/a', state: 's2' }))
    await tick()
    const msg = { type: 'result', provider: 'slack', state: 's2', result: 'ok' } satisfies OAuthMessage
    window.dispatchEvent(new MessageEvent('message', { data: msg, origin: 'https://evil.test' }))
    window.dispatchEvent(new MessageEvent('message', { data: msg, origin: window.location.origin }))
    await expect(flow.promise).resolves.toEqual({ result: 'ok', reason: undefined })
    expect(popup.close).toHaveBeenCalled()
  })

  it('never redirects this tab: a blocked popup resolves `blocked` without calling the API', async () => {
    vi.spyOn(window, 'open').mockReturnValue(null)
    const start = vi.fn()
    const flow = startOAuthPopup('slack', start)
    await expect(flow.promise).resolves.toEqual({ result: 'blocked' })
    expect(start).not.toHaveBeenCalled()
  })

  it('closes the popup when the start call fails, and on cancel', async () => {
    const popup = fakePopup()
    vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    const failed = startOAuthPopup('slack', () => Promise.reject(new Error('conflict')))
    await expect(failed.promise).resolves.toEqual({ result: 'error', reason: 'conflict' })
    expect(popup.close).toHaveBeenCalled()

    const p2 = fakePopup()
    vi.spyOn(window, 'open').mockReturnValue(p2 as unknown as Window)
    const flow = startOAuthPopup('slack', () => new Promise(() => {}))
    flow.cancel()
    await expect(flow.promise).resolves.toEqual({ result: 'cancelled' })
    expect(p2.close).toHaveBeenCalled()
  })
})

describe('OAuthCompletePage (D-21)', () => {
  it('reports the outcome with its state and closes once acknowledged', async () => {
    const close = vi.spyOn(window, 'close').mockImplementation(() => {})
    const opener = new FakeChannel(OAUTH_CHANNEL)
    const got: OAuthMessage[] = []
    opener.onmessage = (ev) => {
      got.push(ev.data as OAuthMessage)
      opener.postMessage({ type: 'ack', state: (ev.data as OAuthMessage).state } satisfies OAuthMessage)
    }
    await mountAgent(OAuthCompletePage, { me: null, route: '/agent/oauth/complete?provider=slack&state=s9&result=error&reason=access_denied' })
    expect(got).toEqual([{ type: 'result', provider: 'slack', state: 's9', result: 'error', reason: 'access_denied' }])
    expect(close).toHaveBeenCalled()
    expect(byTestId('oauth-complete-text')!.textContent).toContain('cancelled')
  })

  it('stays put without an acknowledgement and ignores unknown providers', async () => {
    const close = vi.spyOn(window, 'close').mockImplementation(() => {})
    const h = await mountAgent(OAuthCompletePage, { me: fx.me('admin'), route: '/agent/oauth/complete?provider=evil&state=s1&result=ok' })
    await new Promise((r) => setTimeout(r, 20))
    expect(close).not.toHaveBeenCalled()
    expect(h.router.currentRoute.value.path).toBe('/agent/oauth/complete')
    expect(h.wrapper.find('a').attributes('href')).toBe('/agent/admin/integrations')
  })
})
