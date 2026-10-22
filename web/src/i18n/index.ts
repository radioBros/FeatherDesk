// SPDX-License-Identifier: AGPL-3.0-only

import { createI18n } from 'vue-i18n'
import en from '@/locales/en.json'
import vuetifyEn from 'vuetify/lib/locale/en.js'

export type MessageSchema = typeof en & { $vuetify?: Record<string, unknown> }
export const SUPPORTED_LOCALES = ['en', 'it', 'de', 'fr', 'es'] as const
export type Locale = (typeof SUPPORTED_LOCALES)[number]
export const FALLBACK_LOCALE: Locale = 'en'

export function isLocale(v: unknown): v is Locale {
  return typeof v === 'string' && (SUPPORTED_LOCALES as readonly string[]).includes(v)
}

// English ships in the main bundle as the fallback; every other locale is a
// lazy chunk loaded on demand (FE-003).
export const i18n = createI18n<[MessageSchema], Locale, false>({
  legacy: false,
  locale: FALLBACK_LOCALE,
  fallbackLocale: FALLBACK_LOCALE,
  // Vuetify's own strings (data-table "No data", pagination…) live under `$vuetify`
  // and come from Vuetify's locale packs through the vue-i18n adapter.
  messages: { en: { ...en, $vuetify: vuetifyEn } } as unknown as Record<Locale, MessageSchema>,
  missingWarn: false,
  fallbackWarn: false,
})

type Pack = { default: Record<string, unknown> }
// One app chunk + one Vuetify locale chunk per language, loaded on demand (FE-003, FE-058).
const loaders: Record<Locale, () => Promise<[Pack, Pack]>> = {
  en: () => Promise.resolve([{ default: en }, { default: vuetifyEn }]),
  it: () => Promise.all([import('@/locales/it.json'), import('vuetify/lib/locale/it.js')]),
  de: () => Promise.all([import('@/locales/de.json'), import('vuetify/lib/locale/de.js')]),
  fr: () => Promise.all([import('@/locales/fr.json'), import('vuetify/lib/locale/fr.js')]),
  es: () => Promise.all([import('@/locales/es.json'), import('vuetify/lib/locale/es.js')]),
}

/**
 * Picks the locale: user preference → instance default → browser languages → en.
 */
export function negotiateLocale(
  userPref?: string | null,
  instanceDefault?: string | null,
  browser: readonly string[] = typeof navigator !== 'undefined' ? navigator.languages ?? [navigator.language] : [],
  available: readonly string[] = SUPPORTED_LOCALES,
): Locale {
  const ok = (l?: string | null) => !!l && available.includes(l) && isLocale(l)
  if (ok(userPref)) return userPref as Locale
  if (ok(instanceDefault)) return instanceDefault as Locale
  for (const b of browser) {
    const short = b?.toLowerCase().split('-')[0]
    if (ok(short)) return short as Locale
  }
  return FALLBACK_LOCALE
}

// One load per locale, shared by concurrent switches (a failed load is retried on the next switch).
const mainLoads = new Map<Locale, Promise<void>>([[FALLBACK_LOCALE, Promise.resolve()]])

function loadMainLocale(locale: Locale): Promise<void> {
  let p = mainLoads.get(locale)
  if (!p) {
    p = loaders[locale]().then(([app, vuetify]) => {
      // Merged, never replaced: an area pack may already be merged into this locale.
      i18n.global.mergeLocaleMessage(locale, { ...app.default, $vuetify: vuetify.default } as MessageSchema)
    })
    p.catch(() => mainLoads.delete(locale))
    mainLoads.set(locale, p)
  }
  return p
}

let switchSeq = 0

/**
 * Loads `locale` (main strings, Vuetify strings and every registered area pack)
 * and switches to it. The last call wins: a switch that is still loading when a
 * later one starts resolves without touching the active locale, so a slow load
 * can never overwrite a newer choice.
 */
export async function setLocale(locale: Locale): Promise<void> {
  const seq = ++switchSeq
  await loadMainLocale(locale)
  // Area message packs (e.g. the agent panel) follow the locale switch.
  await Promise.all([...packs.keys()].map((name) => loadPackLocale(name, locale)))
  if (seq !== switchSeq) return
  i18n.global.locale.value = locale
  if (typeof document !== 'undefined') document.documentElement.lang = locale
}

/**
 * Lazy per-area message packs. An area (e.g. the agent panel) keeps its strings
 * out of the main bundle: `loadMessagePack('agent', (l) => import(...))` merges
 * them into the current locale (and `en` as fallback), and `setLocale` loads
 * them for every later switch.
 */
export type PackLoader = (locale: Locale) => Promise<{ default: Record<string, unknown> } | Record<string, unknown>>
const packs = new Map<string, PackLoader>()
const loadedPacks = new Map<string, Promise<void>>()

function loadPackLocale(name: string, locale: Locale): Promise<void> {
  const key = `${name}:${locale}`
  let p = loadedPacks.get(key)
  if (!p) {
    const loader = packs.get(name)!
    p = loader(locale).then((mod) => {
      const messages = ('default' in mod ? mod.default : mod) as Record<string, unknown>
      i18n.global.mergeLocaleMessage(locale, messages as MessageSchema)
    })
    p.catch(() => loadedPacks.delete(key))
    loadedPacks.set(key, p)
  }
  return p
}

export async function loadMessagePack(name: string, loader: PackLoader): Promise<void> {
  if (!packs.has(name)) packs.set(name, loader)
  const current = i18n.global.locale.value as Locale
  const wanted = current === FALLBACK_LOCALE ? [current] : [FALLBACK_LOCALE, current]
  await Promise.all(wanted.map((l) => loadPackLocale(name, l)))
}

export const t = (key: string, named?: Record<string, unknown>): string =>
  named ? i18n.global.t(key, named) : i18n.global.t(key)
