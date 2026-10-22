// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Runtime branding (FE-002, D-13): theme colours, logos, favicon, title,
 * radius and font come from GET /auth/config and can change live
 * (`settings.branding_changed` over the `me` topic).
 */
import type { ThemeDefinition } from 'vuetify'
import type { Branding, ThemeMode } from '@/types'
import { forDark, normalizeHex, onColor } from '@/utils/color'
import { paletteColors } from '@/theme/palette'

type Area = 'portal' | 'agent' | 'kb' | 'setup'

function areaColors(b: Branding, area: Area) {
  const o = area === 'agent' ? b.agent : area === 'portal' || area === 'kb' ? b.portal : undefined
  return {
    primary: normalizeHex(o?.primary ?? b.primary_color) ?? '#1a73e8',
    secondary: normalizeHex(o?.secondary ?? b.secondary_color ?? '#5f6368') ?? '#5f6368',
    accent: normalizeHex(o?.accent ?? b.accent_color ?? '#00897b') ?? '#00897b',
  }
}

export function buildThemes(b: Branding, area: Area = 'portal'): Record<string, ThemeDefinition> {
  const c = areaColors(b, area)
  return {
    light: {
      dark: false,
      // Raised from Vuetify's defaults so labels and hints meet WCAG AA (4.5:1).
      variables: { 'medium-emphasis-opacity': 0.72, 'disabled-opacity': 0.5 },
      colors: {
        // Section hues and AA-safe success/warning/error/info; the brand colours below win on conflicts.
        ...paletteColors(false),
        primary: c.primary,
        secondary: c.secondary,
        accent: c.accent,
        'on-primary': onColor(c.primary),
        'on-secondary': onColor(c.secondary),
        'internal-note': '#fff4d6',
        'on-internal-note': '#4a3500',
        // Tier chips (outlined text): ≥ 4.5:1 on the light surface.
        'tier-1': '#455a64',
        'tier-2': '#3949ab',
        'tier-3': '#5e35b1',
      },
    },
    dark: {
      dark: true,
      variables: { 'medium-emphasis-opacity': 0.78, 'disabled-opacity': 0.55 },
      colors: {
        ...paletteColors(true),
        primary: forDark(c.primary),
        secondary: forDark(c.secondary),
        accent: forDark(c.accent),
        'on-primary': onColor(forDark(c.primary)),
        'on-secondary': onColor(forDark(c.secondary)),
        'internal-note': '#3d3010',
        'on-internal-note': '#ffe8a8',
        // Tier chips (outlined text): ≥ 4.5:1 on the dark surface.
        'tier-1': '#b0bec5',
        'tier-2': '#9fa8da',
        'tier-3': '#b39ddb',
      },
    },
  }
}

const RADIUS: Record<Branding['radius'], string | number> = { sharp: 0, default: 'default', rounded: 'lg' }

export function radiusDefaults(r: Branding['radius'] = 'default') {
  const rounded = RADIUS[r] ?? 'default'
  return {
    global: { rounded },
    // Buttons: tonal by default, no shadows (solid CTAs say `flat` explicitly, see theme/intent.ts).
    VBtn: { rounded, variant: 'tonal', elevation: 0 },
    VBtnToggle: { elevation: 0 },
    VBtnGroup: { elevation: 0 },
    VCard: { rounded },
    // One density and variant for every field (FE-032): outlined keeps labels on the surface colour
    // (contrast-safe), compact keeps selects, text fields, textareas and dates at the same 40px height.
    // Chips inside a field are small so a multiselect never grows taller than a text field.
    VTextField: { rounded, variant: 'outlined', density: 'compact' },
    VTextarea: { rounded, variant: 'outlined', density: 'compact', rows: 1, autoGrow: true },
    VSelect: { rounded, variant: 'outlined', density: 'compact', VChip: { size: 'small' }, color: 'primary' },
    VAutocomplete: { rounded, variant: 'outlined', density: 'compact', VChip: { size: 'small' } },
    VCombobox: { rounded, variant: 'outlined', density: 'compact', VChip: { size: 'small' } },
    VFileInput: { rounded, variant: 'outlined', density: 'compact' },
    VDateInput: { rounded, variant: 'outlined', density: 'compact' },
    VNumberInput: { rounded, variant: 'outlined', density: 'compact' },
    VCheckbox: { density: 'compact', hideDetails: 'auto', color: 'primary' },
    VSwitch: { density: 'compact' },
    VRadioGroup: { density: 'compact', hideDetails: 'auto', color: 'primary' },
    VTabs: { color: "primary" }
  }
}

const FONTS: Record<NonNullable<Branding['font_family']>, string> = {
  system: 'system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif',
  serif: 'Georgia, "Times New Roman", Cambria, serif',
  mono: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
}

export interface BrandableVuetify {
  theme: {
    themes: { value: Record<string, { colors: Record<string, unknown> }> }
    change: (name: string) => unknown
  }
  defaults: { value: Record<string, unknown> | undefined }
}

/** Applies branding to a live Vuetify instance and the document. */
export function applyBranding(v: BrandableVuetify, b: Branding, area: Area = 'portal', doc: Document = document) {
  const built = buildThemes(b, area)
  for (const name of ['light', 'dark'] as const) {
    const live = v.theme.themes.value[name]
    if (live) Object.assign(live.colors, built[name]!.colors)
  }
  v.defaults.value = { ...v.defaults.value, ...radiusDefaults(b.radius) }
  applyDocumentBranding(b, doc)
}

export function applyThemeMode(v: BrandableVuetify, mode: ThemeMode) {
  v.theme.change(mode)
}

/** The FeatherDesk logo by RadioBros: the default whenever no brand logo, icon or favicon is uploaded. */
export const DEFAULT_LOGO_URL = '/featherdesk-logo.svg'

export function applyDocumentBranding(b: Branding, doc: Document = document, pageTitle?: string) {
  doc.title = pageTitle ? `${pageTitle} · ${b.product_name}` : b.product_name
  const fav = b.favicon_url || b.icon_url || DEFAULT_LOGO_URL
  let link = doc.querySelector<HTMLLinkElement>('link#fd-favicon')
  if (!link) {
    link = doc.createElement('link')
    link.id = 'fd-favicon'
    link.rel = 'icon'
    doc.head.appendChild(link)
  }
  link.href = fav || 'data:,'
  doc.documentElement.style.setProperty('--fd-font', FONTS[b.font_family ?? 'system'])
}

/** Picks the logo for the active theme, falling back light ↔ dark ↔ icon ↔ the FeatherDesk logo. */
export function logoFor(b: Branding, dark: boolean): string {
  return (dark ? b.logo_dark_url || b.logo_light_url : b.logo_light_url || b.logo_dark_url) || b.icon_url || DEFAULT_LOGO_URL
}

/** True when no brand logo is uploaded and the default FeatherDesk logo is shown. */
export function isDefaultLogo(b: Branding): boolean {
  return !(b.logo_light_url || b.logo_dark_url || b.icon_url)
}

const THEME_KEY = 'fd.theme'
export function storedThemeMode(): ThemeMode | null {
  try {
    const v = localStorage.getItem(THEME_KEY)
    return v === 'light' || v === 'dark' || v === 'system' ? v : null
  } catch {
    return null
  }
}
export function storeThemeMode(mode: ThemeMode) {
  try {
    localStorage.setItem(THEME_KEY, mode)
  } catch {
    /* private mode: ignore */
  }
}
