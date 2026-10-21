// SPDX-License-Identifier: AGPL-3.0-only

/**
 * The `privacy.*` strings (D-19 §C, FE-080) are their own lazy message pack
 * (src/locales/privacy/<locale>.json), loaded with the portal and agent packs,
 * so the GDPR screens add nothing to the main bundle.
 */
import { loadMessagePack, type Locale } from '@/i18n'

export const loadPrivacyMessages = () => loadMessagePack('privacy', (l: Locale) => import(`../locales/privacy/${l}.json`))
