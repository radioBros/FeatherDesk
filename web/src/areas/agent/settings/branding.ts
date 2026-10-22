// SPDX-License-Identifier: AGPL-3.0-only

/** Branding form model ↔ `BrandingSettings` (FE-047). */
import type { components } from '@/api/schema'

type S = components['schemas']
export type BrandingSettings = S['BrandingSettings']

export interface ColorDraft {
  primary: string
  secondary: string
  accent: string
}

export interface BrandingDraft {
  product_name: string
  company_name: string
  support_url: string
  colors: ColorDraft
  portal_override: boolean
  portal_colors: ColorDraft
  agent_override: boolean
  agent_colors: ColorDraft
  default_theme: 'light' | 'dark' | 'system'
  radius: 'sharp' | 'default' | 'rounded'
  font: NonNullable<BrandingSettings['font']>
  login_title: string
  login_subtitle: string
  login_background_color: string
  welcome_md: string
  new_ticket_intro_md: string
  email_header_color: string
  email_footer_md: string
  privacy_url: string
  terms_url: string
  imprint_url: string
  default_locale: S['Locale']
  available_locales: S['Locale'][]
  powered_by: boolean
  custom_css: string
}

const colors = (c?: S['ColorSet'] | null, fallback?: ColorDraft): ColorDraft => ({
  primary: c?.primary ?? fallback?.primary ?? '',
  secondary: c?.secondary ?? fallback?.secondary ?? '',
  accent: c?.accent ?? fallback?.accent ?? '',
})

export function toDraft(b?: BrandingSettings | null): BrandingDraft {
  const base = colors(b?.colors)
  return {
    product_name: b?.product_name ?? '',
    company_name: b?.company_name ?? '',
    support_url: b?.support_url ?? '',
    colors: base,
    portal_override: !!b?.portal_colors,
    portal_colors: colors(b?.portal_colors, base),
    agent_override: !!b?.agent_colors,
    agent_colors: colors(b?.agent_colors, base),
    default_theme: b?.default_theme ?? 'system',
    radius: b?.radius ?? 'default',
    font: b?.font ?? 'system',
    login_title: b?.login?.title ?? '',
    login_subtitle: b?.login?.subtitle ?? '',
    login_background_color: b?.login?.background_color ?? '',
    welcome_md: b?.portal?.welcome_md ?? '',
    new_ticket_intro_md: b?.portal?.new_ticket_intro_md ?? '',
    email_header_color: b?.email?.header_color ?? '',
    email_footer_md: b?.email?.footer_md ?? '',
    privacy_url: b?.legal?.privacy_url ?? '',
    terms_url: b?.legal?.terms_url ?? '',
    imprint_url: b?.legal?.imprint_url ?? '',
    default_locale: b?.default_locale ?? 'en',
    available_locales: b?.available_locales?.length ? [...b.available_locales] : ['en', 'it', 'de', 'fr', 'es'],
    powered_by: b?.powered_by ?? true,
    custom_css: b?.custom_css ?? '',
  }
}

const opt = (s: string) => (s.trim() ? s.trim() : undefined)
const nul = (s: string) => (s.trim() ? s.trim() : null)
const colorSet = (c: ColorDraft): S['ColorSet'] => ({ primary: c.primary.trim(), secondary: opt(c.secondary), accent: opt(c.accent) })

/** The PUT body: everything except the read-only `assets`. */
export function toSettings(d: BrandingDraft): BrandingSettings {
  return {
    product_name: d.product_name.trim(),
    company_name: d.company_name.trim(),
    support_url: nul(d.support_url),
    colors: colorSet(d.colors),
    portal_colors: d.portal_override ? colorSet(d.portal_colors) : null,
    agent_colors: d.agent_override ? colorSet(d.agent_colors) : null,
    default_theme: d.default_theme,
    radius: d.radius,
    font: d.font,
    login: { title: d.login_title.trim(), subtitle: d.login_subtitle.trim(), background_color: nul(d.login_background_color) },
    portal: { welcome_md: d.welcome_md, new_ticket_intro_md: d.new_ticket_intro_md },
    email: { header_color: opt(d.email_header_color), footer_md: d.email_footer_md },
    legal: { privacy_url: nul(d.privacy_url), terms_url: nul(d.terms_url), imprint_url: nul(d.imprint_url) },
    default_locale: d.default_locale,
    available_locales: d.available_locales,
    powered_by: d.powered_by,
    custom_css: d.custom_css,
  }
}

/** Effective colours of one area for the preview panes. */
export function areaColors(d: BrandingDraft, area: 'portal' | 'agent'): ColorDraft {
  const o = area === 'portal' ? (d.portal_override ? d.portal_colors : null) : d.agent_override ? d.agent_colors : null
  return {
    primary: o?.primary || d.colors.primary,
    secondary: o?.secondary || d.colors.secondary,
    accent: o?.accent || d.colors.accent,
  }
}
