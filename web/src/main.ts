// SPDX-License-Identifier: AGPL-3.0-only

import { createApp, watch } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { i18n, negotiateLocale, setLocale } from './i18n'
import { createAppVuetify } from './plugins/vuetify'
import './styles/main.scss'
import { applyBranding, applyDocumentBranding, storedThemeMode } from './plugins/branding'
import { createAppRouter } from './router'
import { loginPathFor } from './router/guards'
import { useConfigStore } from './stores/config'
import { useSessionStore } from './stores/session'
import { useRealtimeStore } from './stores/realtime'
import { useUiStore } from './stores/ui'
import { t } from './i18n'

function areaOf(path: string): 'portal' | 'agent' | 'kb' | 'setup' {
  if (path.startsWith('/agent')) return 'agent'
  if (path.startsWith('/kb')) return 'kb'
  if (path.startsWith('/setup')) return 'setup'
  return 'portal'
}

/** The browser canvas follows the active theme, so nothing outside the app flashes the other scheme. */
function setColorScheme(scheme: 'light' | 'dark') {
  document.documentElement.style.colorScheme = scheme
}

async function bootstrap() {
  // While /auth/config loads the page is still empty: paint its canvas in the theme the user
  // picked, so a dark-mode user never sees a white page first (FE-002).
  const early = storedThemeMode()
  if (early === 'light' || early === 'dark') setColorScheme(early)

  const app = createApp(App)
  const pinia = createPinia()
  app.use(pinia)

  // Branding and locale are resolved BEFORE mount so there is no flash of default theme (FE-002).
  const config = useConfigStore()
  await config.load()
  const session = useSessionStore()
  if (!config.config.setup_pending) await session.refresh().catch(() => null)

  let stored: string | null = null
  try {
    stored = localStorage.getItem('fd.locale')
  } catch {
    /* ignore */
  }
  await setLocale(negotiateLocale(session.me?.locale ?? stored, config.branding.default_locale))

  const branding = { ...config.branding }
  const mode = storedThemeMode()
  if (mode) branding.default_theme = mode
  const vuetify = createAppVuetify(branding, areaOf(location.pathname) === 'agent' ? 'agent' : 'portal')
  applyDocumentBranding(config.branding)
  watch(
    () => vuetify.theme.current.value.dark,
    (dark) => setColorScheme(dark ? 'dark' : 'light'),
    { immediate: true },
  )

  const router = createAppRouter()
  router.afterEach((to) => {
    const area = to.meta.area ?? 'portal'
    applyBranding(vuetify, config.branding, area)
    applyDocumentBranding(config.branding, document, to.meta.title ? t(to.meta.title) : undefined)
  })

  app.use(i18n).use(vuetify).use(router)

  // Realtime: one socket per tab once signed in; `me` carries branding changes and revocation.
  const rt = useRealtimeStore()
  const ui = useUiStore()
  const startRealtime = () => {
    if (!session.me || rt.client) return
    const c = rt.start()
    c.subscribe('me', { essential: true })
    c.onEvent(async (ev) => {
      if (ev.event === 'settings.branding_changed') {
        await config.load()
        applyBranding(vuetify, config.branding, (router.currentRoute.value.meta.area ?? 'portal'))
      } else if (ev.event === 'session.revoked') {
        ui.notify(t('realtime.sessionRevoked'), 'warning')
        session.clear()
        rt.stop()
        void router.push(loginPathFor(router.currentRoute.value.meta.area, config.config.portal_enabled !== false))
      } else if (ev.event === 'ticket.access_lost') {
        ui.notify(t('realtime.accessLost'), 'warning')
      }
    })
    c.onClose((code) => {
      if (code === 4401) {
        session.clear()
        rt.stop()
      } else if (code === 4403 && session.isCustomer) {
        // The customer portal was switched off while this tab was open.
        session.clear()
        rt.stop()
        void config.load().then(() => router.push(loginPathFor('portal', config.config.portal_enabled !== false)))
      }
    })
  }
  startRealtime()
  router.afterEach(() => {
    if (session.me) startRealtime()
    else if (rt.client) rt.stop()
  })

  await router.isReady().catch(() => null)
  app.mount('#app')
}

void bootstrap()
