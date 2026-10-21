// SPDX-License-Identifier: AGPL-3.0-only

/** Picks a `LocalizedText` entry: current locale → instance default → en → first. */
export function localized(text: Record<string, string> | null | undefined, locale: string, fallback = 'en'): string {
  if (!text) return ''
  return text[locale] || text[fallback] || text.en || Object.values(text).find(Boolean) || ''
}
