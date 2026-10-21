// SPDX-License-Identifier: AGPL-3.0-only

import { HUES, SECTION_HUE, SEMANTIC, paletteColors } from './palette'
import { createAppVuetify } from '@/plugins/vuetify'
import { applyBranding } from '@/plugins/branding'
import { DEFAULT_BRANDING } from '@/stores/config'
import { contrastRatio, hexToRgb, rgbToHex } from '@/utils/color'

const SURFACE = { light: '#ffffff', dark: '#212121' }

/** `fg` at `alpha` over `bg`: the tonal background of a badge, chip or avatar. */
function tint(fg: string, bg: string, alpha: number): string {
  const [f, b] = [hexToRgb(fg), hexToRgb(bg)]
  return rgbToHex(f[0] * alpha + b[0] * (1 - alpha), f[1] * alpha + b[1] * (1 - alpha), f[2] * alpha + b[2] * (1 - alpha))
}

describe('semantic palette', () => {
  for (const theme of ['light', 'dark'] as const) {
    it(`every hue is AA as text on its own tonal tint and on the surface (${theme})`, () => {
      for (const [hue, v] of Object.entries(HUES)) {
        const c = v[theme]
        expect(contrastRatio(c, tint(c, SURFACE[theme], 0.16)), `${hue} on tint`).toBeGreaterThanOrEqual(4.5)
        expect(contrastRatio(c, SURFACE[theme]), `${hue} on surface`).toBeGreaterThanOrEqual(4.5)
      }
    })
    it(`semantic colours work as solid buttons and as tonal text (${theme})`, () => {
      const colors = paletteColors(theme === 'dark')
      for (const [k, c] of Object.entries(SEMANTIC[theme])) {
        expect(contrastRatio(c, colors[`on-${k}`]!), `${k} solid`).toBeGreaterThanOrEqual(4.5)
        expect(contrastRatio(c, tint(c, SURFACE[theme], 0.12)), `${k} tonal`).toBeGreaterThanOrEqual(4.5)
      }
    })
    it(`every section is registered as nav-<section> (${theme})`, () => {
      const colors = paletteColors(theme === 'dark')
      for (const s of Object.keys(SECTION_HUE)) expect(colors[`nav-${s}`]).toMatch(/^#[0-9a-f]{6}$/)
    })
  }
  it('the sections map to the agreed hues', () => {
    expect(SECTION_HUE).toMatchObject({
      tickets: 'blue', kb: 'teal', reports: 'purple', automation: 'amber', customers: 'indigo',
      notifications: 'orange', privacy: 'green', teams: 'cyan', settings: 'pink', branding: 'deepPurple',
      email: 'lightBlue', system: 'blueGrey', queue: 'blueGrey',
    })
  })
  it('both themes carry the palette, and a runtime rebrand keeps it and the brand primary', () => {
    const v = createAppVuetify({ ...DEFAULT_BRANDING, primary_color: '#111111' })
    expect(v.theme.themes.value.light!.colors['nav-tickets']).toBe(HUES.blue.light)
    expect(v.theme.themes.value.dark!.colors['nav-tickets']).toBe(HUES.blue.dark)
    applyBranding(v, { ...DEFAULT_BRANDING, primary_color: '#22aa22' })
    expect(v.theme.themes.value.light!.colors.primary).toBe('#22aa22')
    expect(v.theme.themes.value.light!.colors['nav-kb']).toBe(HUES.teal.light)
    expect(v.theme.themes.value.dark!.colors['nav-kb']).toBe(HUES.teal.dark)
  })
})
