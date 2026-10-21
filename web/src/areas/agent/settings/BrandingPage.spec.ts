// SPDX-License-Identifier: AGPL-3.0-only

import BrandingPage from './BrandingPage.vue'
import BrandingForm from './BrandingForm.vue'
import { mountAgent, fx, json, byTestId, typeInto, click } from '../testing'
import type { components } from '@/api/schema'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const BRANDING: components['schemas']['BrandingSettings'] = {
  product_name: 'Acme Help',
  company_name: 'Acme',
  colors: { primary: '#0d47a1', secondary: '#5f6368', accent: '#00897b' },
  default_theme: 'light',
  radius: 'default',
  font: 'system',
  default_locale: 'en',
  available_locales: ['en', 'it'],
  powered_by: true,
  assets: { logo_light: { slot: 'logo_light', url: '/branding/abc.png', sha256: 'abc', content_type: 'image/png' } },
}

function pickFile(testid: string, file: File) {
  const input = byTestId(testid)!.querySelector('input[type="file"]') as HTMLInputElement
  Object.defineProperty(input, 'files', { value: [file], configurable: true })
  input.dispatchEvent(new Event('change', { bubbles: true }))
}

async function mountPage(api: Record<string, unknown> = {}) {
  return mountAgent(BrandingPage, {
    me: fx.me('admin'),
    api: { 'GET /admin/branding': BRANDING, 'GET /auth/config': { branding: { product_name: 'Acme Help' } }, ...api } as never,
  })
}

describe('BrandingPage (FE-047)', () => {
  it('renders every slot and the form from the saved settings', async () => {
    const h = await mountPage()
    await h.flush()
    for (const s of ['logo_light', 'logo_dark', 'icon', 'favicon', 'email_logo', 'login_background']) expect(byTestId(`slot-${s}`)).toBeTruthy()
    expect((byTestId('brand-product-name')!.querySelector('input') as HTMLInputElement).value).toBe('Acme Help')
    expect(byTestId('slot-logo_light-reset')).toBeTruthy()
    expect(byTestId('slot-icon-reset')).toBeNull()
  })

  it('rejects an SVG email logo client-side with the file_type message and sends nothing', async () => {
    const h = await mountPage()
    await h.flush()
    pickFile('slot-email_logo-input', new File(['<svg/>'], 'logo.svg', { type: 'image/svg+xml' }))
    await h.flush()
    expect(byTestId('slot-email_logo')!.textContent).toContain('This file type is not allowed')
    expect(h.calls(/^POST/)).toHaveLength(0)
  })

  it('rejects a file over 1 MB and uploads a valid one', async () => {
    const h = await mountPage({
      'POST /admin/branding/assets/logo_dark': json({ slot: 'logo_dark', url: '/branding/d.png', sha256: 'd', content_type: 'image/png' }, 201),
    })
    await h.flush()
    pickFile('slot-logo_dark-input', new File([new Uint8Array(1024 * 1024 + 1)], 'big.png', { type: 'image/png' }))
    await h.flush()
    expect(byTestId('slot-logo_dark')!.textContent).toContain('The file is larger than 1 MB')
    expect(h.calls(/^POST/)).toHaveLength(0)
    pickFile('slot-logo_dark-input', new File([new Uint8Array(10)], 'ok.png', { type: 'image/png' }))
    await h.flush()
    expect(h.calls('POST /admin/branding/assets/logo_dark')).toHaveLength(1)
    expect(byTestId('slot-logo_dark-reset')).toBeTruthy()
  })

  it('warns when the primary colour has a contrast below 4.5:1 with white text', async () => {
    const h = await mountPage()
    await h.flush()
    expect(byTestId('brand-primary-contrast-ok')).toBeTruthy()
    await typeInto(byTestId('brand-primary'), '#ffeb3b')
    expect(byTestId('brand-primary-contrast-warning')).toBeTruthy()
    expect(h.calls(/^PUT/)).toHaveLength(0)
  })

  it('updates the preview panes without saving', async () => {
    const h = await mountPage()
    await h.flush()
    const pane = () => byTestId('preview-portal')!.getAttribute('style') ?? ''
    expect(pane()).toContain('#0d47a1')
    await typeInto(byTestId('brand-primary'), '#c62828')
    await typeInto(byTestId('brand-product-name'), 'Globex Support')
    expect(pane()).toContain('#c62828')
    expect(byTestId('preview-portal-bar')!.textContent).toContain('Globex Support')
    expect(h.calls(/^PUT/)).toHaveLength(0)
  })

  it('renders the email preview in an iframe sandboxed without scripts', async () => {
    const h = await mountPage()
    await h.flush()
    const iframe = byTestId('email-preview-iframe') as HTMLIFrameElement
    expect(iframe.hasAttribute('sandbox')).toBe(true)
    expect(iframe.getAttribute('sandbox')).not.toContain('allow-scripts')
    expect(iframe.getAttribute('src')).toMatch(/^\/api\/v1\/admin\/email\/templates\/[^/]+\/preview\?locale=en/)
  })

  it('blocks Save on an empty product name or an invalid colour', async () => {
    const h = await mountPage({ 'PUT /admin/branding': json({ settings: BRANDING, warnings: [] }) })
    await h.flush()
    await typeInto(byTestId('brand-product-name'), '')
    await click(byTestId('branding-save'))
    expect(h.calls('PUT /admin/branding')).toHaveLength(0)
    await typeInto(byTestId('brand-product-name'), 'Acme')
    await typeInto(byTestId('brand-primary'), 'blue')
    await click(byTestId('branding-save'))
    expect(h.wrapper.text()).toContain('Enter a colour like #1A73E8')
    expect(h.calls('PUT /admin/branding')).toHaveLength(0)
  })

  it('saves, shows warnings, reloads the public config and applies the title live', async () => {
    const h = await mountPage({
      'PUT /admin/branding': (_u: URL, init: { body?: any }) =>
        json({ settings: { ...BRANDING, ...init.body }, warnings: [{ field: 'colors.accent', code: 'contrast', message: 'Accent contrast is 2.1:1' }] }),
      'GET /auth/config': { branding: { product_name: 'Globex Support', primary_color: '#c62828' } },
    })
    await h.flush()
    const before = (byTestId('email-preview-iframe') as HTMLIFrameElement).getAttribute('src')
    await typeInto(byTestId('brand-product-name'), 'Globex Support')
    await click(byTestId('branding-save'))
    await h.flush()
    const body = h.calls('PUT /admin/branding')[0]!.body as Record<string, unknown>
    expect(body).toMatchObject({ product_name: 'Globex Support', colors: { primary: '#0d47a1' }, portal_colors: null })
    expect(body).not.toHaveProperty('assets')
    expect(h.calls('GET /auth/config')).toHaveLength(1)
    expect(document.title).toContain('Globex Support')
    expect(byTestId('branding-warnings')!.textContent).toContain('Accent contrast is 2.1:1')
    expect((byTestId('email-preview-iframe') as HTMLIFrameElement).getAttribute('src')).not.toBe(before)
  })
})

describe('BrandingForm (FE-005)', () => {
  it('an empty submit shows the required message and sends nothing', async () => {
    const h = await mountAgent(BrandingForm, { props: { settings: null } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })
})
