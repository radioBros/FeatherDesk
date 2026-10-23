// SPDX-License-Identifier: AGPL-3.0-only

/**
 * OAuth popup install (D-21 §2), shared by every OAuth integration. The flow
 * always runs in a popup, never by navigating this tab: when the browser
 * blocks the popup the outcome is `blocked` and the UI asks to allow popups.
 *
 * The completion page (`/agent/oauth/complete`) reports the outcome, keyed by
 * the OAuth `state`, on two paths (whichever arrives first wins):
 *
 *   1. `window.opener.postMessage(msg, origin)` when the opener survived;
 *   2. a same-origin `BroadcastChannel`, because FeatherDesk sends
 *      `Cross-Origin-Opener-Policy: same-origin`: once the popup has visited
 *      the provider the browser severs `window.opener` (and `popup.closed`
 *      is not reliable), and only the channel still connects the two pages.
 *
 *   completion page → { type: 'result', provider, state, result, reason }
 *   this page       → { type: 'ack', state } on the channel, and closes the
 *                     popup itself when it still has a handle on it
 */
import type { OAuthProvider } from './providers'

export const OAUTH_CHANNEL = 'featherdesk-oauth'

export interface OAuthOutcome {
  result: 'ok' | 'error' | 'cancelled' | 'blocked'
  reason?: string
}

export interface OAuthMessage {
  type: 'result' | 'ack'
  provider?: string
  state: string
  result?: 'ok' | 'error'
  reason?: string
}

export interface OAuthFlow {
  promise: Promise<OAuthOutcome>
  /** Stops waiting (the user gave up) and closes the popup; resolves `cancelled`. */
  cancel: () => void
  /** Resolves the flow from outside, e.g. a status check shows it connected. */
  settle: (o: OAuthOutcome) => void
}

const FEATURES = 'popup,width=640,height=760'

/**
 * Must be called synchronously from the click handler: the blank popup is
 * opened before `start` (an API call) so the browser does not block it.
 */
export function startOAuthPopup(provider: OAuthProvider, start: () => Promise<{ authorize_url: string; state: string }>): OAuthFlow {
  const popup = window.open('', `fd-oauth-${provider}`, FEATURES)
  let state = ''
  let done = false
  let resolveFn: (o: OAuthOutcome) => void = () => {}
  const channel = popup && typeof BroadcastChannel !== 'undefined' ? new BroadcastChannel(OAUTH_CHANNEL) : null
  const flow: OAuthFlow = {
    promise: new Promise<OAuthOutcome>((resolve) => (resolveFn = resolve)),
    cancel: () => {
      finish({ result: 'cancelled' })
      try {
        popup?.close()
      } catch {
        /* severed by COOP: the user closes it */
      }
    },
    settle: (o) => finish(o),
  }
  function finish(o: OAuthOutcome) {
    if (done) return
    done = true
    channel?.close()
    window.removeEventListener('message', onWindowMessage)
    resolveFn(o)
  }
  /** A result for this flow, from either path. */
  function onResult(m: OAuthMessage | undefined) {
    if (!m || m.type !== 'result' || !state || m.state !== state) return
    channel?.postMessage({ type: 'ack', state } satisfies OAuthMessage)
    try {
      popup?.close()
    } catch {
      /* severed by COOP: the popup closes itself on the ack */
    }
    finish({ result: m.result === 'ok' ? 'ok' : 'error', reason: m.reason })
  }
  function onWindowMessage(ev: MessageEvent<OAuthMessage>) {
    if (ev.origin === window.location.origin) onResult(ev.data)
  }
  if (!popup) {
    finish({ result: 'blocked' })
    return flow
  }
  window.addEventListener('message', onWindowMessage)
  if (channel) channel.onmessage = (ev: MessageEvent<OAuthMessage>) => onResult(ev.data)
  start().then(
    (s) => {
      if (done) return
      state = s.state
      popup.location.href = s.authorize_url
      popup.focus()
    },
    (e: unknown) => {
      popup.close()
      finish({ result: 'error', reason: e instanceof Error ? e.message : 'start_failed' })
    },
  )
  return flow
}
