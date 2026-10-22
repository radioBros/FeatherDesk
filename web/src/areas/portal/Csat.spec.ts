// SPDX-License-Identifier: AGPL-3.0-only

import CsatPage from './pages/CsatPage.vue'
import { mockFetch, json } from '@/test/mount'
import { mountPage, requests, settle } from './testing'

afterEach(() => vi.unstubAllGlobals())

describe('CSAT (FE-036)', () => {
  it('the landing page submits the preselected score exactly once, with the email token', async () => {
    const { fn } = mockFetch(() => json({ id: 't1' }))
    const { wrapper } = await mountPage(CsatPage, { path: '/tickets/t1/rate?score=4&token=sig', pattern: '/tickets/:id/rate', me: null })
    await settle(8)
    const reqs = await requests(fn)
    expect(reqs).toHaveLength(1)
    expect(reqs[0]).toMatchObject({ method: 'POST', path: '/portal/tickets/t1/csat', body: { score: 4, csat_token: 'sig' } })
    expect(wrapper.find('[data-testid="csat-recorded"]').exists()).toBe(true)
    await settle(8)
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('without a score nothing is sent until the form is valid (FE-005)', async () => {
    const { fn } = mockFetch(() => json({ id: 't1' }))
    const { wrapper } = await mountPage(CsatPage, { path: '/tickets/t1/rate', pattern: '/tickets/:id/rate' })
    await settle()
    await wrapper.find('[data-testid="csat-form"]').trigger('submit')
    await settle()
    expect(fn).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('This field is required')
    await wrapper.find('[data-testid="csat-score-5"] input').setValue(true)
    await wrapper.find('[data-testid="csat-comment"] textarea').setValue('Great help')
    await wrapper.find('[data-testid="csat-form"]').trigger('submit')
    await settle(8)
    expect((await requests(fn))[0]).toMatchObject({ body: { score: 5, comment: 'Great help' } })
    expect(wrapper.find('[data-testid="csat-done"]').exists()).toBe(true)
  })

  it('an already-rated ticket shows a friendly message', async () => {
    mockFetch(() => json({ type: 'x/conflict', title: 'Conflict', status: 409 }, 409))
    const { wrapper } = await mountPage(CsatPage, { path: '/tickets/t1/rate?score=2', pattern: '/tickets/:id/rate' })
    await settle(8)
    expect(wrapper.find('[data-testid="csat-error"]').text()).toBe('This ticket has already been rated.')
  })
})
