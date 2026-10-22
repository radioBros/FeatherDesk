// SPDX-License-Identifier: AGPL-3.0-only

import MfaStep from './MfaStep.vue'
import { mountAgent, json, byTestId, typeInto, click } from '@/areas/agent/testing'
import { useSessionStore } from '@/stores/session'
import type { MfaChallenge } from './mfa'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
  document.body.innerHTML = ''
})

const ME = { id: 'u1', kind: 'customer', role: 'requester', name: 'Ada', email: 'ada@example.test', csrf_token: 'c2' }
const challenge = (extra: Partial<MfaChallenge> = {}): MfaChallenge => ({
  mfa_required: true,
  mfa_token: 'tok-1',
  methods: ['totp', 'email', 'recovery'],
  enrollment_required: false,
  expires_at: '2026-10-21T10:05:00Z',
  ...extra,
})
const problem = (status: number, extra: object = {}) => json({ type: 'x', title: 'x', status, ...extra }, status)

describe('MfaStep: verify (FE-070)', () => {
  it('sends the TOTP code, stores the session and finishes', async () => {
    const h = await mountAgent(MfaStep, { me: null, props: { challenge: challenge() }, api: { 'POST /auth/mfa/verify': ME } })
    await click(byTestId('mfa-code-submit'))
    expect(h.calls('POST /auth/mfa/verify')).toHaveLength(0) // validate() first
    await typeInto(byTestId('mfa-code'), '123 456')
    await click(byTestId('mfa-code-submit'))
    await h.flush()
    expect(h.calls('POST /auth/mfa/verify')[0]!.body).toEqual({ mfa_token: 'tok-1', method: 'totp', code: '123 456' })
    expect(useSessionStore().me?.id).toBe('u1')
    expect(h.wrapper.emitted('done')).toHaveLength(1)
  })

  it('a wrong code is a field error; a dead challenge asks to sign in again', async () => {
    let n = 0
    const h = await mountAgent(MfaStep, {
      me: null,
      props: { challenge: challenge() },
      api: {
        'POST /auth/mfa/verify': () =>
          ++n === 1 ? problem(422, { errors: [{ field: 'code', code: 'invalid' }] }) : problem(401),
      },
    })
    await typeInto(byTestId('mfa-code'), '000000')
    await click(byTestId('mfa-code-submit'))
    await h.flush()
    expect(byTestId('mfa-code-form')!.textContent).toContain('not valid')
    await click(byTestId('mfa-code-submit'))
    await h.flush()
    expect(byTestId('mfa-expired')).not.toBeNull()
    await click(byTestId('mfa-restart'))
    expect(h.wrapper.emitted('restart')).toHaveLength(1)
  })

  it('recovery codes use their own format rule', async () => {
    const h = await mountAgent(MfaStep, { me: null, props: { challenge: challenge() }, api: { 'POST /auth/mfa/verify': ME } })
    await click(byTestId('mfa-method-recovery'))
    await typeInto(byTestId('mfa-code'), '123456')
    await click(byTestId('mfa-code-submit'))
    expect(h.calls('POST /auth/mfa/verify')).toHaveLength(0)
    expect(document.body.textContent).toContain('Enter a recovery code')
    await typeInto(byTestId('mfa-code'), 'abcd-efgh-jkmn')
    await click(byTestId('mfa-code-submit'))
    await h.flush()
    expect(h.calls('POST /auth/mfa/verify')[0]!.body).toMatchObject({ method: 'recovery', code: 'abcd-efgh-jkmn' })
  })

  it('e-mail: sends a code and blocks resend for 30 s', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const h = await mountAgent(MfaStep, {
      me: null,
      props: { challenge: challenge({ methods: ['email'] }) },
      api: { 'POST /auth/mfa/email/send': { ok: true }, 'POST /auth/mfa/verify': ME },
    })
    await click(byTestId('mfa-send-email'))
    await h.flush()
    expect(h.calls('POST /auth/mfa/email/send')[0]!.body).toEqual({ mfa_token: 'tok-1' })
    expect(byTestId('mfa-email-sent')).not.toBeNull()
    expect((byTestId('mfa-send-email') as HTMLButtonElement).disabled).toBe(true)
    expect(byTestId('mfa-send-email')!.textContent).toContain('30')
    vi.advanceTimersByTime(30_000)
    await h.flush()
    expect((byTestId('mfa-send-email') as HTMLButtonElement).disabled).toBe(false)
    await typeInto(byTestId('mfa-code'), '654321')
    await click(byTestId('mfa-code-submit'))
    await h.flush()
    expect(h.calls('POST /auth/mfa/verify')[0]!.body).toMatchObject({ method: 'email', code: '654321' })
  })
})

describe('MfaStep: forced enrolment (FE-070)', () => {
  it('shows QR + secret, confirms, then shows the recovery codes once', async () => {
    const h = await mountAgent(MfaStep, {
      me: null,
      props: { challenge: challenge({ methods: ['totp'], enrollment_required: true }) },
      api: {
        'POST /auth/mfa/enroll/totp': { secret: 'JBSWY3DPEHPK3PXP', otpauth_uri: 'otpauth://totp/x:ada?secret=JBSWY3DPEHPK3PXP' },
        'POST /auth/mfa/enroll/confirm': { me: ME, recovery_codes: ['aaaa-bbbb-cccc', 'dddd-eeee-ffff'] },
      },
    })
    await click(byTestId('mfa-enroll-totp-start'))
    await h.flush()
    await h.flush()
    expect(byTestId('mfa-secret')!.textContent).toBe('JBSWY3DPEHPK3PXP')
    await vi.waitFor(() => expect(byTestId('mfa-qr')).not.toBeNull())
    await typeInto(byTestId('mfa-code'), '111222')
    await click(byTestId('mfa-code-submit'))
    await h.flush()
    expect(h.calls('POST /auth/mfa/enroll/confirm')[0]!.body).toEqual({ mfa_token: 'tok-1', method: 'totp', code: '111222' })
    expect(document.body.querySelectorAll('[data-testid="mfa-recovery-code"]')).toHaveLength(2)
    expect(useSessionStore().me?.id).toBe('u1')
    expect(h.wrapper.emitted('done')).toBeUndefined()
    await click(byTestId('mfa-continue'))
    expect(h.wrapper.emitted('done')).toHaveLength(1)
  })
})
