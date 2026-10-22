// SPDX-License-Identifier: AGPL-3.0-only

import StoragePage from './StoragePage.vue'
import { mountAgent, fx, byTestId } from '../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('StoragePage (FE-059)', () => {
  it('renders the local fixture', async () => {
    const h = await mountAgent(StoragePage, {
      me: fx.me('admin'),
      api: { 'GET /admin/storage': { backend: 'local', ok: true, used_bytes: 2048, object_count: 3 } },
    })
    await h.flush()
    expect(byTestId('storage-backend')!.textContent).toContain('Local disk')
    expect(byTestId('storage-endpoint')).toBeNull()
    expect(h.wrapper.text()).toContain('2 KB')
  })

  it('renders the S3 fixture without any credential field', async () => {
    const h = await mountAgent(StoragePage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/storage': {
          backend: 's3',
          ok: false,
          endpoint: 'https://s3.example.com',
          bucket: 'tickets',
          last_error: 'connection reset',
          // A server bug must not surface these: the page only renders known fields.
          access_key: 'AKIA-should-not-show',
          secret_key: 'never-show',
        },
      },
    })
    await h.flush()
    expect(byTestId('storage-endpoint')!.textContent).toContain('https://s3.example.com')
    expect(byTestId('storage-bucket')!.textContent).toContain('tickets')
    expect(byTestId('storage-error')!.textContent).toContain('connection reset')
    const html = document.body.innerHTML
    expect(html).not.toMatch(/secret/i)
    expect(html).not.toMatch(/\bkey\b/i)
    expect(html).not.toContain('AKIA')
  })

  it('shows an error state', async () => {
    const h = await mountAgent(StoragePage, { me: fx.me('admin'), api: {} })
    await h.flush()
    expect(byTestId('load-error')).toBeTruthy()
  })
})
