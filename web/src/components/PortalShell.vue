<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, defineAsyncComponent } from 'vue'
import { mdiBookOpenPageVariantOutline, mdiLogin, mdiPlus, mdiTicketOutline } from '@mdi/js'
import { useDisplay } from 'vuetify'
import { useConfigStore } from '@/stores/config'
import { useSessionStore } from '@/stores/session'
import AppBar from './AppBar.vue'
import BrandLogo from './BrandLogo.vue'
import ThemeToggle from './ThemeToggle.vue'
import LocaleSwitcher from './LocaleSwitcher.vue'
import UserMenu from './UserMenu.vue'
import SectionIcon from './SectionIcon.vue'
import ConnectionBanner from './ConnectionBanner.vue'
import { useBarCollapsed } from '@/composables/useAppPrefs'

// The notification bell (D-19 §D) is its own chunk, loaded right after the shell (FE-031 budget).
const NotificationBell = defineAsyncComponent(() => import('@/notifications/NotificationBell.vue'))
const config = useConfigStore()
const session = useSessionStore()
const { xs } = useDisplay()
const barCollapsed = useBarCollapsed()
const b = computed(() => config.branding)
// support.portal_enabled=false: no customer entry point is shown; the public help centre (own `kb_enabled`) stays.
const portalOn = computed(() => config.config.portal_enabled !== false)
const home = computed(() => (portalOn.value ? '/' : config.config.kb_enabled ? '/kb' : '/agent'))
const legal = computed(() =>
  [
    { href: b.value.privacy_url, label: 'nav.privacy' },
    { href: b.value.terms_url, label: 'nav.terms' },
    { href: b.value.imprint_url, label: 'nav.imprint' },
  ].filter((l): l is { href: string; label: string } => !!l.href),
)
</script>

<template>
  <v-app>
    <a class="st-skip" href="#main">{{ $t('nav.skipToContent') }}</a>
    <ConnectionBanner />
    <AppBar>
      <v-app-bar-title>
        <router-link :to="home" class="text-decoration-none text-high-emphasis"><BrandLogo /></router-link>
      </v-app-bar-title>
      <template #append>
        <template v-if="portalOn && session.isCustomer">
          <v-btn to="/tickets" variant="text" data-intent="plain" class="d-none d-sm-flex">
            <template #prepend><SectionIcon section="tickets" :icon="mdiTicketOutline" :size="26" /></template>
            {{ $t('nav.myTickets') }}
          </v-btn>
          <!-- A phone keeps the action as an icon, so the bar never overflows. -->
          <v-btn v-if="xs" to="/tickets/new" :icon="mdiPlus" color="primary" variant="flat" :aria-label="$t('nav.newTicket')" data-testid="new-ticket-icon" />
          <v-btn v-else to="/tickets/new" :prepend-icon="mdiPlus" color="primary" variant="flat">{{ $t('nav.newTicket') }}</v-btn>
        </template>
        <v-btn v-if="config.config.kb_enabled" to="/kb" variant="text" data-intent="plain" class="d-none d-md-flex">
          <template #prepend><SectionIcon section="kb" :icon="mdiBookOpenPageVariantOutline" :size="26" /></template>
          {{ $t('nav.help') }}
        </v-btn>
        <template v-if="!barCollapsed">
          <LocaleSwitcher />
          <ThemeToggle />
        </template>
        <NotificationBell v-if="portalOn && session.isCustomer" area="portal" />
        <UserMenu v-if="session.me" :area="portalOn || !session.isStaff ? 'portal' : 'agent'" />
        <v-btn v-else-if="portalOn" to="/login" variant="tonal">{{ $t('auth.signIn') }}</v-btn>
        <!-- A long label ("Mitarbeiter-Anmeldung") would push the bar off a phone: icon only there. -->
        <v-btn v-else-if="xs" to="/agent/login" :icon="mdiLogin" variant="tonal" :aria-label="$t('nav.staffSignIn')" data-testid="staff-sign-in" />
        <v-btn v-else to="/agent/login" variant="tonal" data-testid="staff-sign-in">{{ $t('nav.staffSignIn') }}</v-btn>
      </template>
    </AppBar>
    <v-main id="main">
      <slot />
    </v-main>
    <v-footer class="text-caption text-medium-emphasis justify-center flex-wrap ga-4" data-testid="portal-footer">
      <span v-if="b.company_name">© {{ new Date().getFullYear() }} {{ b.company_name }}</span>
      <a v-for="l in legal" :key="l.label" :href="l.href" target="_blank" rel="noopener" class="text-medium-emphasis">{{ $t(l.label) }}</a>
      <span v-if="b.powered_by" data-testid="powered-by">
        <a href="https://radiobros.com/" target="_blank" rel="noopener" class="text-medium-emphasis">
          {{ $t('app.poweredBy', { name: 'SimpleTickets', author: 'RadioBros' }) }}
        </a>
      </span>
    </v-footer>
  </v-app>
</template>

<style scoped>
.st-skip { position: absolute; left: -9999px; }
.st-skip:focus { left: 8px; top: 8px; z-index: 10000; background: rgb(var(--v-theme-surface)); padding: 8px; }
</style>
