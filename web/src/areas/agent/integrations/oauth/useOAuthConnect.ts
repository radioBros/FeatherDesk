// SPDX-License-Identifier: AGPL-3.0-only

import { computed, onBeforeUnmount, shallowRef } from 'vue'
import { startOAuthPopup, type OAuthFlow, type OAuthOutcome } from './oauthPopup'
import type { OAuthProvider } from './providers'

export interface OAuthConnectOptions {
  provider: OAuthProvider
  /** POSTs the provider's start endpoint: `{ authorize_url, state }`. */
  start: () => Promise<{ authorize_url: string; state: string }>
  /**
   * Fallback when the popup's message is lost: on window focus, `check()`
   * returns the connection marker (e.g. `connected_at`); a value different
   * from `marker()` at click time counts as connected.
   */
  marker?: () => string | null | undefined
  check?: () => Promise<string | null | undefined>
}

/**
 * The OAuth popup install as a composable (D-21): `connect()` must be called
 * straight from the click handler (it opens the popup synchronously) and
 * resolves with the outcome. `connecting` drives a waiting state; `cancel()`
 * stops waiting. Unmounting cancels.
 */
export function useOAuthConnect(opts: OAuthConnectOptions) {
  const flow = shallowRef<OAuthFlow | null>(null)
  let stopFocus = () => {}

  function connect(): Promise<OAuthOutcome> {
    const before = opts.marker?.() ?? null
    const f = startOAuthPopup(opts.provider, opts.start)
    flow.value = f
    if (opts.check) {
      const onFocus = async () => {
        const now = await opts.check!().catch(() => null)
        if (now && now !== before) f.settle({ result: 'ok' })
      }
      window.addEventListener('focus', onFocus)
      stopFocus = () => window.removeEventListener('focus', onFocus)
    }
    return f.promise.finally(() => {
      stopFocus()
      if (flow.value === f) flow.value = null
    })
  }

  const cancel = () => flow.value?.cancel()
  onBeforeUnmount(cancel)
  return { connect, cancel, connecting: computed(() => !!flow.value) }
}
