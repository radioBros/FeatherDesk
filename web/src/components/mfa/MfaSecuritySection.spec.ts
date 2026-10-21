// SPDX-License-Identifier: AGPL-3.0-only

import MfaSecuritySection from './MfaSecuritySection.vue'
import { mountAgent, json, byTestId, typeInto, click } from '@/areas/agent/testing'
import { settleConfirm } from '@/composables/useConfirm'
import type { MfaStatus } from './mfa'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const FUTURE = new Date(Date.now() + 600_000).toISOString()
const status = (extra: Partial<MfaStatus> = {}): MfaStatus => ({
  available: true,
  enabled: false,
  required: false,
  totp_enabled: false,
  totp_enabled_at: null,
  email_enabled: false,
  email_available: true,
  recovery_codes_remaining: 0,
  reauth_expires_at: null,
  ...extra,
})

describe('MfaSecuritySection (FE-072)', () => {
  it('explains that SSO accounts get two-step verification from their IdP', async () => {
    await mountAgent(MfaSecuritySection, { api: { 'GET /me/mfa': status({ available: false }) } })
    expect(byTestId('mfa-unavailable')).not.toBeNull()
    expect(byTestId('mfa-totp-setup')).toBeNull()
  })

  it('asks for the password first, then sets up TOTP and shows the recovery codes once', async () => {
    const h = await mountAgent(MfaSecuritySection, {
      api: {
        'GET /me/mfa': status({ required: true }),
        'POST /me/mfa/reauth': status({ reauth_expires_at: FUTURE }),
        'POST /me/mfa/totp/setup': { secret: 'JBSWY3DPEHPK3PXP', otpauth_uri: 'otpauth://totp/x:a?secret=JBSWY3DPEHPK3PXP' },
        'POST /me/mfa/totp/confirm': { status: status({ enabled: true, totp_enabled: true, recovery_codes_remaining: 10 }), recovery_codes: ['aaaa-bbbb-cccc'] },
      },
    })
    expect(byTestId('mfa-required')).not.toBeNull()
    await click(byTestId('mfa-totp-setup'))
    expect(h.calls('POST /me/mfa/totp/setup')).toHaveLength(0)
    await click(byTestId('mfa-reauth-submit'))
    expect(h.calls('POST /me/mfa/reauth')).toHaveLength(0) // empty password: validate() stops it
    await typeInto(byTestId('mfa-reauth-password'), 'my-password-123')
    await click(byTestId('mfa-reauth-submit'))
    await h.flush()
    expect(h.calls('POST /me/mfa/reauth')[0]!.body).toEqual({ password: 'my-password-123' })
    expect(h.calls('POST /me/mfa/totp/setup')).toHaveLength(1)
    await vi.waitFor(() => expect(byTestId('mfa-qr')).not.toBeNull())
    expect(byTestId('mfa-secret')!.textContent).toBe('JBSWY3DPEHPK3PXP')
    await typeInto(byTestId('mfa-code'), '123456')
    await click(byTestId('mfa-code-submit'))
    await h.flush()
    expect(h.calls('POST /me/mfa/totp/confirm')[0]!.body).toEqual({ code: '123456' })
    expect(byTestId('mfa-recovery-code')!.textContent).toBe('aaaa-bbbb-cccc')
    await click(byTestId('mfa-codes-done'))
    await h.flush()
    expect(h.calls('GET /me/mfa').length).toBeGreaterThanOrEqual(2)
  })

  it('skips the password while the re-auth window is open; turning off asks to confirm', async () => {
    const on = status({ enabled: true, totp_enabled: true, email_enabled: true, recovery_codes_remaining: 7, reauth_expires_at: FUTURE })
    const h = await mountAgent(MfaSecuritySection, {
      api: { 'GET /me/mfa': on, 'DELETE /me/mfa/email': () => json({ ...on, email_enabled: false }), 'POST /me/mfa/recovery-codes': { recovery_codes: ['zzzz-yyyy-xxxx'] } },
    })
    expect(byTestId('mfa-row-recovery')!.textContent).toContain('7')
    await click(byTestId('mfa-email-disable'))
    settleConfirm(true)
    await h.flush()
    await h.flush()
    expect(h.calls('DELETE /me/mfa/email')).toHaveLength(1)
    expect(byTestId('mfa-reauth-form')).toBeNull()
    await click(byTestId('mfa-recovery-regenerate'))
    settleConfirm(true)
    await h.flush()
    await h.flush()
    expect(byTestId('mfa-recovery-code')!.textContent).toBe('zzzz-yyyy-xxxx')
  })

  it('cannot turn on e-mail codes without SMTP', async () => {
    await mountAgent(MfaSecuritySection, { api: { 'GET /me/mfa': status({ email_available: false }) } })
    expect((byTestId('mfa-email-enable') as HTMLButtonElement).disabled).toBe(true)
  })
})
