<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Agent panel shell (FE-019): queues with live count badges (REST once, then
 * the `counts` WebSocket topic, REST again only on resync), saved views, the
 * admin navigation (visible per the route guard, never a role comparison),
 * global search (`#1042` jumps to the ticket) and the `?` shortcut help.
 * Kept light: it is part of the agent area's initial JS (FE-031).
 */
import { computed, defineAsyncComponent, onMounted, ref, watch } from 'vue'
import type { RouteLocationNormalized } from 'vue-router'
import { useRouter } from 'vue-router'
import {
  mdiAccountMultipleOutline,
  mdiBookOpenPageVariantOutline,
  mdiBookmarkOutline,
  mdiChartBar,
  mdiCog,
  mdiDomain,
  mdiInboxArrowDown,
  mdiKeyboardOutline,
  mdiMagnify,
  mdiMenu,
  mdiNumeric1,
  mdiNumeric2,
  mdiNumeric3,
  mdiPlus,
  mdiShareVariantOutline,
  mdiTicketAccount,
  mdiTicketOutline,
  mdiTrayFull,
} from '@mdi/js'
import { useDisplay } from 'vuetify'
import { useSessionStore } from '@/stores/session'
import { useConfigStore } from '@/stores/config'
import { useRealtime } from '@/composables/useRealtime'
import { decide } from '@/router/guards'
import { adminNav, agentRoutes } from '@/router/agent-routes'
import AppBar from '@/components/AppBar.vue'
import BrandLogo from '@/components/BrandLogo.vue'
import ThemeToggle from '@/components/ThemeToggle.vue'
import LocaleSwitcher from '@/components/LocaleSwitcher.vue'
import UserMenu from '@/components/UserMenu.vue'
import NavItem from '@/components/NavItem.vue'
import NavHeader from '@/components/NavHeader.vue'
import ConnectionBanner from '@/components/ConnectionBanner.vue'
import { useCountsStore } from './counts'
import { useProjectsStore } from '@/stores/projects'
import { useViewsStore } from './tickets/list/views'
import { useShortcuts } from './shared/useShortcuts'
import { useBarCollapsed } from '@/composables/useAppPrefs'

// FE-060: loaded when the user menu first opens, never part of the layout chunk.
const AvailabilityToggle = defineAsyncComponent(() => import('./automation/AvailabilityToggle.vue'))
// The notification bell (D-19 §D) is its own chunk, loaded right after the shell (FE-031 budget).
// The admin entries (about 25 section icons) load when the drawer renders them, not with the shell.
const AdminNavItems = defineAsyncComponent(() => import('./AdminNavItems.vue'))
const NotificationBell = defineAsyncComponent(() => import('@/notifications/NotificationBell.vue'))
// The project switcher (and VChip with it) loads only once the user has more than one active project.
const ProjectSwitcher = defineAsyncComponent(() => import('./ProjectSwitcher.vue'))
// The `?` shortcut help (and VDialog with it) loads on first open.
const ShortcutsHelp = defineAsyncComponent(() => import('./shared/ShortcutsHelp.vue'))

const session = useSessionStore()
const config = useConfigStore()
const counts = useCountsStore()
const projects = useProjectsStore()
const views = useViewsStore()
const router = useRouter()
const { mdAndUp } = useDisplay()
const barCollapsed = useBarCollapsed()
const drawer = ref(mdAndUp.value)
const search = ref('')
const help = ref(false)
const helpLoaded = ref(false)
watch(help, (open) => {
  if (open) helpLoaded.value = true
})

useRealtime(['counts'], { onCounts: counts.apply, onResync: () => void counts.load() })
useRealtime(['me'], { essential: true })
onMounted(() => {
  void projects.load().then(() => counts.load())
  if (!views.loaded) void views.load()
})

const queues = computed(() => [
  { to: '/agent/tickets?view=mine', icon: mdiTicketAccount, title: 'agent.layout.view.mine', count: counts.counts.mine, testid: 'queue-mine' },
  { to: '/agent/tickets?view=unassigned', icon: mdiInboxArrowDown, title: 'agent.layout.view.unassigned', count: counts.counts.unassigned, testid: 'queue-unassigned' },
  { to: '/agent/tickets?view=triage', icon: mdiTrayFull, title: 'agent.layout.view.triage', count: counts.counts.triage, testid: 'queue-triage' },
  { to: '/agent/tickets?view=all', icon: mdiTicketOutline, title: 'agent.layout.view.all', count: undefined, testid: 'queue-all' },
])
const TIER_ICONS = [mdiNumeric1, mdiNumeric2, mdiNumeric3]
const tiers = computed(() =>
  session.visibleTiers.map((t) => ({ tier: t, icon: TIER_ICONS[Math.min(Math.max(t, 1), 3) - 1]!, label: config.tierLabel(t), count: counts.counts.tier[String(t)] ?? 0 })),
)

/**
 * Admin entries the guard would let through. Resolved against the agent route
 * table (not the live router) so the decision is the guard's own, UX only.
 */
const layoutRoute = agentRoutes.find((r) => r.path === '/agent')
function allowed(to: string): boolean {
  const child = layoutRoute?.children?.find((c) => `/agent/${c.path}` === to)
  if (!child) return false
  const fake = { path: to, fullPath: to, meta: { ...(layoutRoute?.meta ?? {}), ...(child.meta ?? {}) } } as unknown as RouteLocationNormalized
  return decide(fake, { me: session.me, setupPending: false }) === true
}
const GROUPS = ['people', 'support', 'channels', 'system'] as const
const adminGroups = computed(() =>
  GROUPS.map((g) => ({ group: g, items: adminNav.filter((i) => i.group === g && allowed(i.to)) })).filter((g) => g.items.length),
)

function onSearch() {
  const q = search.value.trim()
  if (!q) return
  const m = /^#?(\d+)$/.exec(q)
  const ref = /^([A-Za-z][A-Za-z0-9]*-\d+)$/.exec(q)
  search.value = ''
  void router.push(m ? `/agent/tickets/${m[1]}` : ref ? `/agent/tickets/${ref[1]!.toUpperCase()}` : { path: '/agent/tickets', query: { q } })
}

useShortcuts({ '?': () => (help.value = true) })
</script>

<template>
  <v-app>
    <a class="st-skip" href="#main">{{ $t('nav.skipToContent') }}</a>
    <ConnectionBanner />
    <AppBar>
      <template #prepend>
        <v-app-bar-nav-icon :icon="mdiMenu" :aria-label="$t('nav.toggleMenu')" @click="drawer = !drawer" />
      </template>
      <v-app-bar-title class="flex-grow-0">
        <router-link to="/agent/tickets" class="text-decoration-none text-high-emphasis"><BrandLogo /></router-link>
      </v-app-bar-title>
      <v-text-field
        v-model="search"
        :placeholder="$t('agent.nav.search')"
        :aria-label="$t('agent.nav.searchLabel')"
        :prepend-inner-icon="mdiMagnify"
        density="compact"
        variant="solo-filled"
        flat
        hide-details
        single-line
        class="st-search"
        data-testid="global-search"
        @keydown.enter="onSearch"
      />
      <template #append>
        <ProjectSwitcher v-if="projects.showSwitcher" />
        <v-btn v-if="mdAndUp" :icon="mdiKeyboardOutline" variant="text" color="nav-system" :aria-label="$t('agent.nav.shortcuts')" data-testid="shortcuts-open" @click="help = true" />
        <template v-if="!barCollapsed">
          <LocaleSwitcher />
          <ThemeToggle />
        </template>
        <NotificationBell area="agent" />
        <UserMenu area="agent"><AvailabilityToggle /></UserMenu>
      </template>
    </AppBar>

    <v-navigation-drawer v-model="drawer" :permanent="mdAndUp" width="264" data-testid="agent-drawer">
      <div class="pa-3">
        <v-btn color="primary" block :prepend-icon="mdiPlus" to="/agent/tickets/new" data-testid="nav-new-ticket">{{ $t('nav.newTicket') }}</v-btn>
      </div>
      <!-- A list of links, not a listbox: the items keep their link role (FE-032, axe aria-required-children). -->
      <v-list density="compact" nav role="none">
        <NavHeader section="tickets">{{ $t('agent.nav.queues') }}</NavHeader>
        <NavItem v-for="q in queues" :key="q.to" :to="q.to" :icon="q.icon" section="tickets" :title="$t(q.title)" exact :data-testid="q.testid">
          <template v-if="q.count !== undefined" #append>
            <v-badge :content="q.count" :model-value="q.count > 0" color="primary" inline data-testid="queue-count" />
          </template>
        </NavItem>
        <NavHeader section="teams">{{ $t('tier.label') }}</NavHeader>
        <NavItem
          v-for="t in tiers"
          :key="t.tier"
          :to="`/agent/tickets?view=tier&tier=${t.tier}`"
          :icon="t.icon"
          section="teams"
          :title="t.label"
          exact
          :data-testid="`queue-tier-${t.tier}`"
        >
          <template #append><v-badge :content="t.count" :model-value="t.count > 0" color="secondary" inline data-testid="tier-count" /></template>
        </NavItem>
        <template v-if="views.views.length">
          <NavHeader section="branding">{{ $t('agent.nav.savedViews') }}</NavHeader>
          <NavItem
            v-for="v in views.views"
            :key="v.id"
            :to="`/agent/tickets?view_id=${v.id}`"
            :icon="v.shared ? mdiShareVariantOutline : mdiBookmarkOutline"
            section="branding"
            :title="v.name"
            exact
            data-testid="nav-view"
          />
        </template>
        <v-divider class="my-2" />
        <NavItem to="/agent/customers" :icon="mdiAccountMultipleOutline" section="customers" :title="$t('nav.customers')" />
        <NavItem to="/agent/organizations" :icon="mdiDomain" section="customers" :title="$t('nav.organizations')" />
        <NavItem v-if="config.config.kb_enabled" to="/agent/kb" :icon="mdiBookOpenPageVariantOutline" section="kb" :title="$t('nav.knowledgeBase')" />
        <NavItem to="/agent/reports" :icon="mdiChartBar" section="reports" :title="$t('nav.reports')" />
        <v-list-group v-if="adminGroups.length" value="admin" data-testid="nav-admin">
          <template #activator="{ props: p, isOpen }">
            <!-- A disclosure button for the admin links (FE-032). -->
            <NavItem v-bind="p" role="button" :aria-expanded="isOpen" :aria-selected="undefined" :icon="mdiCog" section="settings" :title="$t('nav.admin')" />
          </template>
          <AdminNavItems :groups="adminGroups" />
        </v-list-group>
      </v-list>
    </v-navigation-drawer>

    <v-main id="main">
      <!-- FE-050: admins learn that notifications are off until SMTP works. -->
      <v-alert
        v-if="session.isAdmin && !config.config.email_enabled"
        type="warning"
        variant="tonal"
        density="compact"
        rounded="0"
        data-testid="email-off-banner"
      >
        {{ $t('agent.emailBanner.text') }}
        <template #append>
          <v-btn size="small" variant="text" to="/agent/admin/email">{{ $t('agent.emailBanner.action') }}</v-btn>
        </template>
      </v-alert>
      <router-view />
    </v-main>

    <ShortcutsHelp v-if="helpLoaded" v-model="help" />
  </v-app>
</template>

<style scoped>
.st-skip { position: absolute; left: -9999px; }
.st-skip:focus { left: 8px; top: 8px; z-index: 10000; background: rgb(var(--v-theme-surface)); padding: 8px; }
.st-search { flex: 1 1 0; min-width: 0; max-width: 420px; margin-inline: 8px; }
</style>
