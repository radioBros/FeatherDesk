// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Route-level hooks for the portal, KB and setup areas (FE-050, FE-031).
 * Tiny and dependency-free so the router can import them statically without
 * pulling any view. Area strings are lazy message packs
 * (src/locales/<area>/<locale>.json), kept out of the main bundle.
 */
import { useConfigStore } from '@/stores/config'
import { loadMessagePack, type Locale } from '@/i18n'
import { loadPrivacyMessages } from '@/i18n/privacy'
import { loadNotificationMessages } from '@/i18n/notifications'

/** Email-backed flows (register, forgot password, magic link) exist only when SMTP works. */
export function requireEmail() {
  return useConfigStore().config.email_enabled ? true : { path: '/login' }
}

/** Self-registration additionally needs the customer audience to allow it. */
export function requireRegistration() {
  const c = useConfigStore().config
  return c.email_enabled && c.customer.self_registration !== false ? true : { path: '/login' }
}

/** The portal pack, plus the `privacy.*` pack (register consent, "My data"; FE-080). */
export const loadPortalMessages = () =>
  Promise.all([loadMessagePack('portal', (l: Locale) => import(`../../locales/portal/${l}.json`)), loadPrivacyMessages(), loadNotificationMessages()]).then(() => undefined)
export const loadKbMessages = () => loadMessagePack('kb', (l: Locale) => import(`../../locales/kb/${l}.json`))
export const loadSetupMessages = () => loadMessagePack('setup', (l: Locale) => import(`../../locales/setup/${l}.json`))

/** Route `beforeEnter` wrapper: a failed pack load never blocks navigation (English keys still resolve as keys). */
const soft = (load: () => Promise<void>) => () => load().then(() => true, () => true)
export const withPortalMessages = soft(loadPortalMessages)
export const withKbMessages = soft(loadKbMessages)
export const withSetupMessages = soft(loadSetupMessages)
