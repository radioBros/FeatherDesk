// SPDX-License-Identifier: AGPL-3.0-only

import SystemPage from './SystemPage.vue'
import { mountAgent, fx, byTestId } from '../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('SystemPage (FE-029)', () => {
  it('renders version, database, OIDC and SMTP status', async () => {
    const h = await mountAgent(SystemPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/system': {
          version: '1.0.0',
          commit: 'abc123',
          uptime_seconds: 90061,
          db: { engine: 'postgres', schema_version: 42, size_bytes: 5 * 1024 * 1024 },
          oidc: { staff: { configured: true, discovery_ok: false }, customer: { configured: false } },
          smtp: { configured: true, ok: true },
          ws: { connections: 3 },
          queue: { deferred: { retrying: 1, dead: 2 }, queues: [] },
        },
      },
    })
    await h.flush()
    const text = h.wrapper.text()
    expect(text).toContain('1.0.0')
    expect(text).toContain('PostgreSQL')
    expect(text).toContain('5 MB')
    expect(byTestId('sys-oidc-staff')!.textContent).toContain('Provider unreachable')
    expect(byTestId('sys-smtp')!.textContent).toContain('Working')
    expect(text).toContain('1 jobs retrying, 2 failed')
    expect(byTestId('sys-email-link')!.getAttribute('href')).toBe('/agent/admin/email')
  })
  it('shows an error state', async () => {
    const h = await mountAgent(SystemPage, { me: fx.me('admin'), api: {} })
    await h.flush()
    expect(byTestId('load-error')).toBeTruthy()
  })
})
