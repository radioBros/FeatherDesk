// SPDX-License-Identifier: AGPL-3.0-only

import { useI18n } from 'vue-i18n'

/**
 * Human text for an OAuth failure code: the provider's own string
 * (`agent.integrations.<provider>.reasons.<code>`), else the shared one
 * (`agent.integrations.oauth.reasons.<code>`), else "error code <code>".
 */
export function useOAuthReason() {
  const { t, te } = useI18n()
  return (provider: string, code?: string): string => {
    if (!code) return t('agent.integrations.oauth.reasons.other', { code: '?' })
    for (const k of [`agent.integrations.${provider}.reasons.${code}`, `agent.integrations.oauth.reasons.${code}`]) {
      if (te(k)) return t(k)
    }
    return t('agent.integrations.oauth.reasons.other', { code })
  }
}
