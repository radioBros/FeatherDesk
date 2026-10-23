// SPDX-License-Identifier: AGPL-3.0-only

/**
 * OAuth providers known to the SPA (D-21). Adding an integration that installs
 * through the popup = one entry here + its config page; the popup helper,
 * `useOAuthConnect`, `OAuthConnectButton` and `/agent/oauth/complete` are shared.
 *
 * The server side mirrors it: `internal/integrations/oauthflow` (state store,
 * completion redirect) and one callback route per provider.
 */
export const OAUTH_PROVIDERS = {
  slack: { name: 'Slack', configPath: '/agent/admin/integrations/slack' },
} as const

export type OAuthProvider = keyof typeof OAUTH_PROVIDERS

export const isOAuthProvider = (v: string): v is OAuthProvider => Object.hasOwn(OAUTH_PROVIDERS, v)
