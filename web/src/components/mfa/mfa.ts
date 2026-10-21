// SPDX-License-Identifier: AGPL-3.0-only

/** Shared MFA helpers for the login step, the profile section and the admin views (D-19 §B, FE-070…). */
import type { components } from '@/api/schema'

export type MfaChallenge = components['schemas']['MfaChallenge']
export type MfaStatus = components['schemas']['MfaStatus']
export type MfaMethod = components['schemas']['MfaMethod']
export type MfaTotpSetup = components['schemas']['MfaTotpSetup']

/** Login-type responses are `Me | MfaChallenge`: true for the challenge. */
export function isMfaChallenge(x: unknown): x is MfaChallenge {
  return !!x && typeof x === 'object' && 'mfa_required' in x && (x as { mfa_required: unknown }).mfa_required === true
}

/** Offers the recovery codes as a plain-text file. */
export function downloadRecoveryCodes(codes: string[], product: string) {
  const text = `${product} — recovery codes\n\n${codes.join('\n')}\n`
  const url = URL.createObjectURL(new Blob([text], { type: 'text/plain' }))
  const a = document.createElement('a')
  a.href = url
  a.download = 'recovery-codes.txt'
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
