// SPDX-License-Identifier: AGPL-3.0-only

import UserMfaCard from './UserMfaCard.vue'
import { mountAgent, fx, byTestId, click } from '../../testing'
import { settleConfirm } from '@/composables/useConfirm'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const ON = { applies: true, enabled: true, totp_enabled: true, email_enabled: false, recovery_codes_remaining: 9 }

describe('UserMfaCard (FE-073)', () => {
  it('admin resets a user after confirming', async () => {
    let state = ON
    const h = await mountAgent(UserMfaCard, {
      me: fx.me('admin'),
      props: { userId: 'u-7' },
      api: {
        'GET /admin/users/u-7/mfa': () => new Response(JSON.stringify(state), { headers: { 'Content-Type': 'application/json' } }),
        'POST /admin/users/u-7/mfa/reset': () => {
          state = { ...ON, enabled: false, totp_enabled: false, recovery_codes_remaining: 0 }
          return new Response(null, { status: 204 })
        },
      },
    })
    expect(byTestId('user-mfa-state')!.textContent).toBe('On')
    await click(byTestId('user-mfa-reset'))
    settleConfirm(true)
    await h.flush()
    await h.flush()
    expect(h.calls('POST /admin/users/u-7/mfa/reset')).toHaveLength(1)
    expect(byTestId('user-mfa-state')!.textContent).toBe('Off')
    expect((byTestId('user-mfa-reset') as HTMLButtonElement).disabled).toBe(true)
  })

  it('renders nothing for non-admins and asks nothing', async () => {
    const h = await mountAgent(UserMfaCard, { me: fx.me('supervisor'), props: { userId: 'u-7' }, api: { 'GET /admin/users/u-7/mfa': ON } })
    expect(byTestId('user-mfa')).toBeNull()
    expect(h.calls('GET /admin/users/u-7/mfa')).toHaveLength(0)
  })

  it('SSO accounts have no reset', async () => {
    await mountAgent(UserMfaCard, { me: fx.me('admin'), props: { userId: 'u-8' }, api: { 'GET /admin/users/u-8/mfa': { ...ON, applies: false, enabled: false } } })
    expect(byTestId('user-mfa-state')!.textContent).toContain('identity provider')
    expect(byTestId('user-mfa-reset')).toBeNull()
  })
})
