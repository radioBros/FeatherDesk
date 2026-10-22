// SPDX-License-Identifier: AGPL-3.0-only

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { apiFetch } from '@/api/client'
import type { AuthConfig, Branding, AreaColors } from '@/types'
import type { components } from '@/api/schema'

type ApiBranding = components['schemas']['BrandingSettings']
export type PortalProject = components['schemas']['PortalProject']

export const DEFAULT_BRANDING: Branding = {
  product_name: 'FeatherDesk',
  company_name: 'RadioBros',
  primary_color: '#1A73E8',
  secondary_color: '#5F6368',
  accent_color: '#00897B',
  default_theme: 'system',
  radius: 'default',
  font_family: 'system',
  powered_by: true,
  default_locale: 'en',
}

export const DEFAULT_CONFIG: AuthConfig = {
  staff: { local: true, oidc: false },
  customer: { local: true, oidc: false, self_registration: true },
  branding: DEFAULT_BRANDING,
  tier_labels: { '1': 'L1', '2': 'L2', '3': 'L3' },
  email_enabled: false,
  setup_pending: false,
  upload: { max_bytes: 25 * 1024 * 1024, allowed_types: [], max_files: 10 },
  message_max_length: 65536,
  customer_urgent_allowed: false,
  kb_enabled: true,
  portal_enabled: true,
}

/** Public instance configuration from GET /auth/config (FE-007). */
export const useConfigStore = defineStore('config', () => {
  const config = ref<AuthConfig>(structuredClone(DEFAULT_CONFIG))
  const loaded = ref(false)
  /** The `/p/{key}` entry's project (D-20): branding overrides; null everywhere else. */
  const project = ref<PortalProject | null>(null)

  async function load(): Promise<AuthConfig> {
    try {
      const data = await apiFetch<Partial<AuthConfig>>('/auth/config')
      config.value = mergeConfig(data)
    } catch {
      config.value = structuredClone(DEFAULT_CONFIG)
    }
    loaded.value = true
    return config.value
  }

  /** Loads one project's public portal view; null when unknown or not open to customers. */
  async function loadProject(key: string): Promise<PortalProject | null> {
    try {
      const data = await apiFetch<{ project?: PortalProject | null }>(`/auth/config?project=${encodeURIComponent(key)}`)
      project.value = data?.project ?? null
    } catch {
      project.value = null
    }
    return project.value
  }
  function clearProject() {
    project.value = null
  }

  function setBranding(b: Partial<Branding>) {
    config.value = { ...config.value, branding: { ...config.value.branding, ...b } }
  }

  const branding = computed(() => config.value.branding)
  const tierLabel = (tier: number | string) => config.value.tier_labels[String(tier)] ?? `L${tier}`

  return { config, loaded, branding, project, load, loadProject, clearProject, setBranding, tierLabel }
})

const FONT_FAMILIES = ['system', 'serif', 'mono'] as const

/**
 * `GET /auth/config` sends branding as the API's `BrandingSettings` (nested:
 * `colors`, `portal_colors`, `assets.*.url`, `login`, `portal`, `legal`,
 * `available_locales`, `font`). The SPA works with the flat `Branding` shape,
 * so the API shape is flattened here, once, for every consumer (FE-002/FE-050).
 * A value that is already flat (tests, `setBranding`) passes through unchanged.
 */
export function brandingFromApi(raw: unknown): Partial<Branding> {
  if (!raw || typeof raw !== 'object') return {}
  if (!('colors' in raw)) return raw as Partial<Branding>
  const b = raw as Partial<ApiBranding>
  const asset = (slot: keyof NonNullable<ApiBranding['assets']>) => b.assets?.[slot]?.url ?? null
  const colors = (c: ApiBranding['colors'] | null | undefined): AreaColors | undefined =>
    c ? { primary: c.primary, secondary: c.secondary, accent: c.accent } : undefined
  const font = FONT_FAMILIES.find((f) => f === b.font) ?? 'system'
  const out: Partial<Branding> = {
    product_name: b.product_name,
    company_name: b.company_name,
    support_url: b.support_url ?? undefined,
    primary_color: b.colors?.primary,
    secondary_color: b.colors?.secondary,
    accent_color: b.colors?.accent,
    portal: colors(b.portal_colors),
    agent: colors(b.agent_colors),
    default_theme: b.default_theme,
    radius: b.radius,
    font_family: font,
    logo_light_url: asset('logo_light'),
    logo_dark_url: asset('logo_dark'),
    icon_url: asset('icon'),
    favicon_url: asset('favicon'),
    login_background_url: asset('login_background'),
    login_title: b.login?.title,
    login_subtitle: b.login?.subtitle,
    login_background_color: b.login?.background_color ?? null,
    portal_welcome_md: b.portal?.welcome_md,
    new_ticket_intro_md: b.portal?.new_ticket_intro_md,
    privacy_url: b.legal?.privacy_url ?? null,
    terms_url: b.legal?.terms_url ?? null,
    imprint_url: b.legal?.imprint_url ?? null,
    powered_by: b.powered_by,
    default_locale: b.default_locale,
    locales: b.available_locales,
  }
  for (const k of Object.keys(out) as (keyof Branding)[]) if (out[k] === undefined) delete out[k]
  return out
}

export function mergeConfig(data: Partial<AuthConfig> | undefined): AuthConfig {
  const d = data ?? {}
  return {
    ...DEFAULT_CONFIG,
    ...d,
    staff: { ...DEFAULT_CONFIG.staff, ...d.staff },
    customer: { ...DEFAULT_CONFIG.customer, ...d.customer },
    branding: { ...DEFAULT_BRANDING, ...brandingFromApi(d.branding) },
    tier_labels: { ...DEFAULT_CONFIG.tier_labels, ...d.tier_labels },
    upload: { ...DEFAULT_CONFIG.upload, ...d.upload },
  }
}
