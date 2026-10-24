// SPDX-License-Identifier: AGPL-3.0-only

import PwaIconsCard from './PwaIconsCard.vue'
import { settleConfirm } from '@/composables/useConfirm'
import { mountAgent, fx, json, byTestId } from '../testing'
import type { components } from '@/api/schema'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const BASE: components['schemas']['BrandingSettings'] = {
  product_name: 'Acme',
  company_name: 'Acme',
  colors: { primary: '#3f51b5' },
  default_theme: 'system',
  default_locale: 'en',
  available_locales: ['en'],
  powered_by: true,
  assets: {
    logo_light: { slot: 'logo_light', url: '/branding/abc.png', sha256: 'abc', content_type: 'image/png' },
  },
}

const mockCanvas = () => {
  const toBlob = vi.fn((cb: (b: Blob | null) => void) => cb(new Blob([new Uint8Array([137, 80])], { type: 'image/png' })))
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
    fillStyle: '',
    fillRect: vi.fn(),
    drawImage: vi.fn(),
  } as unknown as CanvasRenderingContext2D)
  vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation(toBlob as never)
  vi.spyOn(globalThis, 'Image').mockImplementation(function ImageMock(this: HTMLImageElement) {
    setTimeout(() => {
      Object.defineProperty(this, 'width', { value: 100, configurable: true })
      Object.defineProperty(this, 'height', { value: 100, configurable: true })
      this.onload?.(new Event('load'))
    }, 0)
    return this
  } as unknown as typeof Image)
}

describe('PwaIconsCard', () => {
  it('posts four PNG parts with source on generate', async () => {
    mockCanvas()
    const h = await mountAgent(PwaIconsCard, {
      me: fx.me('admin'),
      props: { settings: BASE },
      api: {
        'POST /admin/branding/pwa-icons': (_u, init) => {
          const body = init.body as FormData
          expect(body.get('source')).toBe('abc.png')
          for (const k of ['icon_192', 'icon_512', 'maskable_512', 'apple_180']) {
            const part = body.get(k)
            expect(part).toBeInstanceOf(Blob)
          }
          return json({ icon_192: 'x.png', icon_512: 'y.png', maskable_512: 'z.png', apple_180: 'a.png', source: 'abc.png', generated_at: '2026-01-01T00:00:00Z' })
        },
      },
    })
    await h.flush()
    byTestId('pwa-icons-generate')!.click()
    await h.flush()
    await h.flush()
    expect(h.calls('POST /admin/branding/pwa-icons')).toHaveLength(1)
  })

  it('calls DELETE on reset', async () => {
    const h = await mountAgent(PwaIconsCard, {
      me: fx.me('admin'),
      props: {
        settings: {
          ...BASE,
          pwa_icons: { icon_192: 'x.png', source: 'old.png', generated_at: '2026-01-01T00:00:00Z' },
        },
      },
      api: { 'DELETE /admin/branding/pwa-icons': new Response(null, { status: 204 }) },
    })
    await h.flush()
    expect(byTestId('pwa-icons-status')!.getAttribute('data-stale')).toBe('true')
    byTestId('pwa-icons-reset')!.click()
    await h.flush()
    await settleConfirm(true)
    await h.flush()
    expect(h.calls('DELETE /admin/branding/pwa-icons')).toHaveLength(1)
  })
})
