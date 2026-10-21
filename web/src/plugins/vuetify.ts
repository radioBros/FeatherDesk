// SPDX-License-Identifier: AGPL-3.0-only

import { createVuetify, type ThemeDefinition } from 'vuetify'
import { aliases, mdi } from 'vuetify/iconsets/mdi-svg'
import { createVueI18nAdapter } from 'vuetify/locale/adapters/vue-i18n'
import { useI18n, type I18n } from 'vue-i18n'
import { i18n } from '@/i18n'
import 'vuetify/styles'
import { buildThemes, radiusDefaults } from './branding'
import type { Branding } from '@/types'
import { DEFAULT_BRANDING } from '@/stores/config'
import IntentBtn from '@/components/IntentBtn'

export type AppVuetify = ReturnType<typeof createVuetify>

/** Creates Vuetify with the theme already built from branding (no flash of default colours). */
export function createAppVuetify(branding: Branding = DEFAULT_BRANDING, area: 'portal' | 'agent' = 'portal') {
  const themes: Record<string, ThemeDefinition> = buildThemes(branding, area)
  return createVuetify({
    icons: { defaultSet: 'mdi', aliases, sets: { mdi } },
    // The adapter is typed for a string locale; ours is the narrower Locale union.
    locale: { adapter: createVueI18nAdapter({ i18n: i18n as unknown as I18n<any, {}, {}, string, false>, useI18n }) },
    theme: { defaultTheme: branding.default_theme ?? 'system', themes },
    // `v-btn` resolves to IntentBtn (vite.config `autoImport.ignore`): buttons by meaning, theme/intent.ts.
    components: { VBtn: IntentBtn },
    defaults: radiusDefaults(branding.radius),
  })
}
