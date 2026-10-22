// SPDX-License-Identifier: AGPL-3.0-only

/**
 * The theme and language choices behind the app bar's buttons, shared by the
 * standalone buttons (ThemeToggle, LocaleSwitcher) and the user menu, which
 * takes them over on a phone so the bar never overflows.
 */
import { computed } from 'vue'
import { useDisplay, useTheme } from 'vuetify'
import { useI18n } from 'vue-i18n'
import { mdiThemeLightDark, mdiWeatherNight, mdiWhiteBalanceSunny } from '@mdi/js'
import { storeThemeMode } from '@/plugins/branding'
import { SUPPORTED_LOCALES, setLocale, type Locale } from '@/i18n'
import { useConfigStore } from '@/stores/config'
import { useSessionStore } from '@/stores/session'
import type { ThemeMode } from '@/types'

export const THEME_MODES: { value: ThemeMode; icon: string; label: string }[] = [
  { value: 'light', icon: mdiWhiteBalanceSunny, label: 'app.themeLight' },
  { value: 'dark', icon: mdiWeatherNight, label: 'app.themeDark' },
  { value: 'system', icon: mdiThemeLightDark, label: 'app.themeSystem' },
]

export function useThemeMode() {
  const theme = useTheme()
  function set(mode: ThemeMode) {
    theme.change(mode)
    storeThemeMode(mode)
  }
  return { modes: THEME_MODES, set }
}

export const LOCALE_NAMES: Record<Locale, string> = { en: 'English', it: 'Italiano', de: 'Deutsch', fr: 'Français', es: 'Español' }

export function useLocalePicker() {
  const { locale } = useI18n()
  const config = useConfigStore()
  const available = computed(() => SUPPORTED_LOCALES.filter((l) => !config.branding.locales?.length || config.branding.locales.includes(l)))
  async function pick(l: Locale) {
    await setLocale(l)
    try {
      localStorage.setItem('fd.locale', l)
    } catch {
      /* ignore */
    }
  }
  return { locale, available, names: LOCALE_NAMES, pick }
}

/**
 * True on a phone with a signed-in user: the theme and language buttons leave
 * the app bar and live in the user menu instead (anonymous visitors have no
 * menu, so they keep the buttons).
 */
export function useBarCollapsed() {
  const { xs } = useDisplay()
  const session = useSessionStore()
  return computed(() => xs.value && !!session.me)
}
