// SPDX-License-Identifier: AGPL-3.0-only

/**
 * The account-security strings, `mfa.*` (two-step sign-in, D-19 §B) and `mcp.*`
 * (the MCP connection help on the API tokens pages), are their own lazy message
 * pack (src/locales/account/<locale>.json), loaded with the portal and agent
 * packs, so they add nothing to the main bundle (FE-031). Every screen that
 * shows them (sign-in second step, profile security, tokens, admin MFA cards)
 * is a portal or agent route.
 */
import { loadMessagePack, type Locale } from '@/i18n'

export const loadAccountMessages = () => loadMessagePack('account', (l: Locale) => import(`../locales/account/${l}.json`))
