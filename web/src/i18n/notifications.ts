// SPDX-License-Identifier: AGPL-3.0-only

/**
 * The `notifications.*` strings (D-19 §D, FE-090…FE-096) are their own lazy
 * message pack (src/locales/notifications/<locale>.json), loaded with the
 * portal and agent packs, so the bell adds no strings to the main bundle.
 */
import { loadMessagePack, type Locale } from '@/i18n'

export const loadNotificationMessages = () => loadMessagePack('notifications', (l: Locale) => import(`../locales/notifications/${l}.json`))
