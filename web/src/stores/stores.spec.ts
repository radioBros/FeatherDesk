// SPDX-License-Identifier: AGPL-3.0-only

import { setActivePinia, createPinia } from 'pinia'
import { useConfigStore, mergeConfig, DEFAULT_CONFIG } from './config'
import { useSessionStore } from './session'
import { useUiStore } from './ui'
import { getCsrfToken } from '@/api/client'
import { mockFetch, json } from '@/test/mount'

beforeEach(() => setActivePinia(createPinia()))
afterEach(() => vi.unstubAllGlobals())

describe('config store (FE-007)', () => {
  it('loads /auth/config and merges with defaults', async () => {
    mockFetch(() => json({ email_enabled: true, branding: { product_name: 'Acme Help', primary_color: '#ff0000' }, tier_labels: { '2': 'Specialist' } }))
    const s = useConfigStore()
    await s.load()
    expect(s.config.email_enabled).toBe(true)
    expect(s.branding.product_name).toBe('Acme Help')
    expect(s.branding.radius).toBe('default')
    expect(s.tierLabel(2)).toBe('Specialist')
    expect(s.tierLabel(1)).toBe('L1')
  })
  it('falls back to defaults when the request fails', async () => {
    mockFetch(() => json({}, 500))
    const s = useConfigStore()
    await s.load()
    expect(s.config).toEqual(DEFAULT_CONFIG)
    expect(s.loaded).toBe(true)
  })
  it('mergeConfig keeps nested defaults', () => {
    expect(mergeConfig({ upload: { max_bytes: 5 } as never }).upload.max_files).toBe(10)
  })
  it('flattens the API BrandingSettings shape of /auth/config (FE-002/FE-050)', () => {
    const api = {
      product_name: 'ACME Desk', company_name: 'ACME', support_url: null,
      colors: { primary: '#C62828', secondary: '#607D8B', accent: '#00BFA5' },
      portal_colors: { primary: '#112233', secondary: '#445566', accent: '#778899' }, agent_colors: null,
      default_theme: 'dark', radius: 'rounded', font: 'inter',
      login: { title: 'Welcome', subtitle: 'Sign in', background_color: '#000000' },
      portal: { welcome_md: 'Hi **there**', new_ticket_intro_md: 'Tell us more' },
      legal: { privacy_url: 'https://acme.test/p', terms_url: null, imprint_url: null },
      default_locale: 'it', available_locales: ['it', 'en'], powered_by: false,
      assets: { logo_light: { slot: 'logo_light', url: '/branding/logo_light?v=1', content_type: 'image/png', sha256: 'x' }, logo_dark: null, icon: null, favicon: null, email_logo: null, login_background: null },
    }
    const b = mergeConfig({ branding: api as never }).branding
    expect(b).toMatchObject({
      product_name: 'ACME Desk', company_name: 'ACME', primary_color: '#C62828', secondary_color: '#607D8B', accent_color: '#00BFA5',
      portal: { primary: '#112233' }, default_theme: 'dark', radius: 'rounded', font_family: 'system',
      login_title: 'Welcome', login_subtitle: 'Sign in', login_background_color: '#000000',
      portal_welcome_md: 'Hi **there**', new_ticket_intro_md: 'Tell us more', privacy_url: 'https://acme.test/p', terms_url: null,
      default_locale: 'it', locales: ['it', 'en'], powered_by: false, logo_light_url: '/branding/logo_light?v=1', logo_dark_url: null,
    })
    expect(b.agent).toBeUndefined()
  })
})

describe('session store (FE-007)', () => {
  it('stores /me and the CSRF token', async () => {
    mockFetch(() => json({ id: 'u1', kind: 'staff', role: 'admin', tier: 3, name: 'Ada', email: 'a@x.io', csrf_token: 'c1' }))
    const s = useSessionStore()
    await s.refresh()
    expect(s.isStaff).toBe(true)
    expect(s.isAdmin).toBe(true)
    expect(s.isSupervisor).toBe(true)
    expect(getCsrfToken()).toBe('c1')
  })
  it('clears on 401', async () => {
    mockFetch(() => json({ title: 'no' }, 401))
    const s = useSessionStore()
    s.set({ id: 'u', kind: 'customer', role: 'requester', name: 'x', email: 'x@y.z', csrf_token: 'z' })
    await s.refresh()
    expect(s.me).toBeNull()
    expect(getCsrfToken()).toBeNull()
    expect(s.checked).toBe(true)
  })
  it('can() reads only allowed_actions — never derives rules', () => {
    const s = useSessionStore()
    s.set({ id: 'u', kind: 'staff', role: 'admin', name: 'x', email: 'x@y.z', csrf_token: 'z' })
    expect(s.can('escalate', { allowed_actions: ['reply'] })).toBe(false)
    expect(s.can('reply', { allowed_actions: ['reply'] })).toBe(true)
    expect(s.can('reply', null)).toBe(false)
  })
  it('logout returns the IdP redirect and clears', async () => {
    mockFetch(() => json({ redirect_url: 'https://idp/logout' }))
    const s = useSessionStore()
    s.set({ id: 'u', kind: 'staff', role: 'agent', name: 'x', email: 'x@y.z', csrf_token: 'z' })
    expect(await s.logout()).toBe('https://idp/logout')
    expect(s.me).toBeNull()
  })
})

describe('ui store', () => {
  it('queues and dismisses toasts', () => {
    const ui = useUiStore()
    ui.notify('a')
    ui.notify('b', 'error')
    expect(ui.toasts.length).toBe(2)
    ui.dismiss(ui.toasts[0]!.id)
    expect(ui.toasts[0]!.text).toBe('b')
  })
})
