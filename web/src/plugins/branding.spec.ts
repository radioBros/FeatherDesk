// SPDX-License-Identifier: AGPL-3.0-only

import { buildThemes, applyDocumentBranding, logoFor, radiusDefaults, applyBranding } from './branding'
import { contrastRatio, forDark, normalizeHex, onColor, shade } from '@/utils/color'
import { DEFAULT_BRANDING } from '@/stores/config'
import type { Branding } from '@/types'

const b = (over: Partial<Branding> = {}): Branding => ({ ...DEFAULT_BRANDING, ...over })

describe('runtime branding (FE-002)', () => {
  it('builds light and dark themes from the brand colours', () => {
    const t = buildThemes(b({ primary_color: '#0D47A1' }))
    expect(t.light!.colors!.primary).toBe('#0d47a1')
    expect(t.light!.colors!['on-primary']).toBe('#ffffff')
    // dark variant is lightened until readable on a dark surface
    expect(contrastRatio(t.dark!.colors!.primary as string, '#121212')).toBeGreaterThanOrEqual(4.5)
  })
  it('applies per-area overrides', () => {
    const t = buildThemes(b({ primary_color: '#111111', agent: { primary: '#22aa22' } }), 'agent')
    expect(t.light!.colors!.primary).toBe('#22aa22')
    expect(buildThemes(b({ primary_color: '#111111', agent: { primary: '#22aa22' } }), 'portal').light!.colors!.primary).toBe('#111111')
  })
  it('swaps title and favicon', () => {
    applyDocumentBranding(b({ product_name: 'Acme Help', favicon_url: '/branding/abc.png' }), document, 'Tickets')
    expect(document.title).toBe('Tickets · Acme Help')
    expect(document.querySelector<HTMLLinkElement>('link#st-favicon')!.getAttribute('href')).toBe('/branding/abc.png')
  })
  it('picks the logo for the theme with fallbacks', () => {
    expect(logoFor(b({ logo_light_url: 'L', logo_dark_url: 'D' }), true)).toBe('D')
    expect(logoFor(b({ logo_light_url: 'L' }), true)).toBe('L')
    expect(logoFor(b({ icon_url: 'I' }), false)).toBe('I')
    expect(logoFor(b(), false)).toBeNull()
  })
  it('maps radius to Vuetify defaults', () => {
    expect(radiusDefaults('sharp').VBtn.rounded).toBe(0)
    expect(radiusDefaults('rounded').VCard.rounded).toBe('lg')
  })
  it('applyBranding updates a live instance in place', () => {
    const live = {
      theme: { themes: { value: { light: { colors: { primary: '#000' } }, dark: { colors: { primary: '#000' } } } }, change: vi.fn() },
      defaults: { value: {} as Record<string, unknown> | undefined },
    }
    applyBranding(live, b({ primary_color: '#ff0000', radius: 'sharp' }), 'portal', document)
    expect(live.theme.themes.value.light.colors.primary).toBe('#ff0000')
    expect((live.defaults.value as any).VBtn.rounded).toBe(0)
  })
})

describe('colour helpers', () => {
  it('normalizes hex', () => {
    expect(normalizeHex('#ABC')).toBe('#aabbcc')
    expect(normalizeHex('nope')).toBeNull()
  })
  it('contrast and on-colour', () => {
    expect(Math.round(contrastRatio('#ffffff', '#000000'))).toBe(21)
    expect(onColor('#ffff00')).toBe('#000000')
    expect(onColor('#000080')).toBe('#ffffff')
  })
  it('shade and forDark', () => {
    expect(shade('#000000', 1)).toBe('#ffffff')
    expect(shade('#ffffff', -1)).toBe('#000000')
    expect(contrastRatio(forDark('#000033'), '#121212')).toBeGreaterThanOrEqual(4.5)
  })
})
