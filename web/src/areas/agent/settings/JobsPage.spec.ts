// SPDX-License-Identifier: AGPL-3.0-only

import JobsPage from './JobsPage.vue'
import { mountAgent, fx, json, byTestId, click } from '../testing'
import { settleConfirm, confirmState } from '@/composables/useConfirm'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const JOB = { id: 'j-1', kind: 'notify.email', attempts: 8, status: 'dead', last_error: 'smtp: 535 auth failed', created_at: fx.iso }

describe('JobsPage (FE-042)', () => {
  it('lists dead jobs and shows the error', async () => {
    const h = await mountAgent(JobsPage, { me: fx.me('admin'), api: { 'GET /admin/jobs': fx.page([JOB]) } })
    await h.flush()
    expect(h.calls('GET /admin/jobs')[0]!.url.searchParams.get('status')).toBe('dead')
    expect(h.wrapper.findAll('[data-testid="job-row"]')).toHaveLength(1)
    await click(byTestId('job-details'))
    expect(byTestId('job-error')!.textContent).toContain('535 auth failed')
  })

  it('retries after confirm and updates the row', async () => {
    const h = await mountAgent(JobsPage, {
      me: fx.me('admin'),
      api: { 'GET /admin/jobs': fx.page([JOB]), 'POST /admin/jobs/j-1/retry': json({ ...JOB, status: 'retrying', attempts: 0 }) },
    })
    await h.flush()
    await click(byTestId('job-retry'))
    expect(confirmState().open).toBe(true)
    expect(h.calls('POST /admin/jobs/j-1/retry')).toHaveLength(0)
    settleConfirm(true)
    await h.flush()
    expect(h.calls('POST /admin/jobs/j-1/retry')).toHaveLength(1)
    expect(byTestId('job-status')!.textContent).toContain('Retrying')
  })

  it('does nothing when the confirm is cancelled', async () => {
    const h = await mountAgent(JobsPage, { me: fx.me('admin'), api: { 'GET /admin/jobs': fx.page([JOB]) } })
    await h.flush()
    await click(byTestId('job-retry'))
    settleConfirm(false)
    await h.flush()
    expect(h.calls('POST /admin/jobs/j-1/retry')).toHaveLength(0)
  })

  it('shows the empty state', async () => {
    const h = await mountAgent(JobsPage, { me: fx.me('admin'), api: { 'GET /admin/jobs': fx.page([]) } })
    await h.flush()
    expect(h.wrapper.text()).toContain('No jobs here')
  })
})
