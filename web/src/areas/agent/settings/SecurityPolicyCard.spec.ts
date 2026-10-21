// SPDX-License-Identifier: AGPL-3.0-only

import SecurityPolicyCard from './SecurityPolicyCard.vue'
import { mountAgent, fx, json, byTestId, click } from '../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('SecurityPolicyCard (FE-074)', () => {
  it('loads the policy and saves a new one', async () => {
    const h = await mountAgent(SecurityPolicyCard, {
      me: fx.me('admin'),
      api: { 'GET /admin/security': { mfa_required: 'none' }, 'PATCH /admin/security': (_u, init) => json(init.body) },
    })
    await h.flush()
    expect(byTestId('security-mfa-required')!.textContent).toContain('Nobody')
    h.wrapper.findComponent({ name: 'VSelect' }).vm.$emit('update:modelValue', 'staff')
    await h.flush()
    await click(byTestId('security-save'))
    await h.flush()
    expect(h.calls('PATCH /admin/security')[0]!.body).toEqual({ mfa_required: 'staff' })
  })
})
