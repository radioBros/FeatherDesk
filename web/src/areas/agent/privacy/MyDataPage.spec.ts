// SPDX-License-Identifier: AGPL-3.0-only

import MyDataPage from './MyDataPage.vue'
import { mountAgent, fx, json, byTestId, click } from '../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('agent "My data" (FE-083)', () => {
  it('lets staff export their own data, but never request deletion', async () => {
    let requests: unknown[] = []
    const h = await mountAgent(MyDataPage, {
      me: fx.me('agent'),
      api: {
        'GET /me/privacy': () => json({ requests, consents: [], can_request_erasure: false, export_days: 7 }),
        'POST /me/privacy/export': () => {
          requests = [{ id: 'r-1', type: 'export', status: 'pending', user_id: 'u-me', requested_by: 'u-me', include_internal: false, mode: null, file_size: 0,
            reason: '', error: '', decided_by: null, decided_at: null, completed_at: null, expires_at: null, download_url: null, created_at: '2026-10-20T10:00:00Z' }]
          return json(requests[0], 202)
        },
      },
    })
    await h.flush()
    expect(byTestId('my-data-erasure')).toBeNull()
    expect(byTestId('my-data-empty')).not.toBeNull()
    await click(byTestId('export-request'))
    await h.flush()
    expect(h.calls('POST /me/privacy/export')).toHaveLength(1)
    expect(byTestId('export-pending')).not.toBeNull()
    expect((byTestId('export-request') as HTMLButtonElement).disabled).toBe(true)
  })
})
