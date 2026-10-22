// SPDX-License-Identifier: AGPL-3.0-only

/**
 * The semantic palette: one hue per app section, each with a light-theme and a
 * dark-theme variant, registered as Vuetify theme colours named `nav-<section>`
 * (`buildThemes` in `plugins/branding.ts`). The brand `primary` stays the main
 * accent and the colour of primary actions; these hues only colour section
 * icons, badges, chips and avatars.
 *
 * Contrast (WCAG AA 4.5:1) of each hue as text on its own 16 % tonal tint over
 * the surface is asserted by `palette.spec.ts`, so any pair of them is safe for
 * chips and icon badges in both themes.
 */
import { onColor } from '@/utils/color'

export type Hue =
  | 'blue' | 'teal' | 'purple' | 'amber' | 'indigo' | 'orange' | 'green'
  | 'cyan' | 'pink' | 'deepPurple' | 'lightBlue' | 'blueGrey' | 'red' | 'brown'

/** Light-theme variant is deep (text on white and on tints), dark-theme variant is pale. */
export const HUES: Record<Hue, { light: string; dark: string }> = {
  blue: { light: '#0d5cb6', dark: '#90caf9' },
  teal: { light: '#00695c', dark: '#80cbc4' },
  purple: { light: '#7b1fa2', dark: '#e1a8ec' },
  amber: { light: '#855000', dark: '#ffd54f' },
  indigo: { light: '#303f9f', dark: '#b1b9e6' },
  orange: { light: '#a63700', dark: '#ffb74d' },
  green: { light: '#276a2b', dark: '#93d296' },
  cyan: { light: '#005f70', dark: '#80deea' },
  pink: { light: '#a31250', dark: '#f8a5c2' },
  deepPurple: { light: '#512da8', dark: '#c5b3e8' },
  lightBlue: { light: '#01579b', dark: '#81d4fa' },
  blueGrey: { light: '#455a64', dark: '#bccad2' },
  red: { light: '#b71c1c', dark: '#f5a8a8' },
  brown: { light: '#6d4c41', dark: '#d7b9ab' },
}

/** App section -> hue. */
export const SECTION_HUE = {
  tickets: 'blue',
  kb: 'teal',
  reports: 'purple',
  automation: 'amber',
  customers: 'indigo',
  notifications: 'orange',
  privacy: 'green',
  teams: 'cyan',
  settings: 'pink',
  branding: 'deepPurple',
  email: 'lightBlue',
  system: 'blueGrey',
  queue: 'blueGrey',
  danger: 'red',
  projects: 'brown',
} as const satisfies Record<string, Hue>

export type Section = keyof typeof SECTION_HUE

/** Vuetify theme colour name of a section, usable as `color="nav-tickets"`. */
export const sectionColor = (s: Section): string => `nav-${s}`

/** Ticket status, priority and tier chips: richer, distinct colours (theme colour names). */
export const STATUS_COLOR = {
  new: 'nav-teams',
  awaiting_reply: 'nav-notifications',
  open: 'nav-tickets',
  pending: 'nav-automation',
  on_hold: 'nav-reports',
  resolved: 'nav-privacy',
  closed: 'nav-system',
} as const
export const PRIORITY_COLOR = { low: 'nav-system', normal: 'nav-tickets', high: 'nav-notifications', urgent: 'nav-danger' } as const

/**
 * Semantic action colours, contrast-checked for white (light) or black (dark)
 * label text on a solid button and for the colour as tonal text.
 */
export const SEMANTIC = {
  light: { success: '#2b742f', warning: '#a15600', error: '#c62828', info: '#0a639f' },
  dark: { success: '#81c784', warning: '#ffca28', error: '#f28b82', info: '#64b5f6' },
} as const

/** The palette as Vuetify theme colours for one theme (`nav-*`, `on-nav-*` and the semantic colours). */
export function paletteColors(dark: boolean): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [section, hue] of Object.entries(SECTION_HUE)) {
    const c = HUES[hue][dark ? 'dark' : 'light']
    out[`nav-${section}`] = c
    out[`on-nav-${section}`] = onColor(c)
  }
  const sem = SEMANTIC[dark ? 'dark' : 'light']
  for (const [k, v] of Object.entries(sem)) {
    out[k] = v
    out[`on-${k}`] = onColor(v)
  }
  return out
}

/** Intent -> Vuetify colour and variant (see `components/IntentBtn.vue`). */
export type Intent = 'primary' | 'positive' | 'warning' | 'danger' | 'secondary' | 'neutral' | 'plain'
export const INTENT_STYLE: Record<Exclude<Intent, 'plain'>, { color: string; variant: 'flat' | 'tonal' }> = {
  primary: { color: 'primary', variant: 'flat' },
  positive: { color: 'success', variant: 'flat' },
  warning: { color: 'warning', variant: 'flat' },
  danger: { color: 'error', variant: 'flat' },
  secondary: { color: 'primary', variant: 'tonal' },
  neutral: { color: 'secondary', variant: 'tonal' },
}

const AVATAR_SECTIONS: Section[] = ['tickets', 'kb', 'reports', 'automation', 'customers', 'notifications', 'privacy', 'teams', 'settings', 'branding', 'email']

/** A stable section colour per person, so avatars in tables and menus are told apart. */
export function avatarColor(name: string | null | undefined): string {
  let h = 0
  for (const ch of name ?? '') h = (h * 31 + ch.charCodeAt(0)) >>> 0
  return sectionColor(AVATAR_SECTIONS[h % AVATAR_SECTIONS.length]!)
}
