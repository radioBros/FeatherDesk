// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Agent panel routes (`/agent/**`, FE-008/FE-019…FE-059). Every page is its own
 * lazy chunk, so the initial agent JS is only the layout + login (FE-031), and
 * the admin pages load on demand. The agent strings are a lazy message pack
 * (src/locales/agent/<locale>.json) loaded before any agent route renders.
 */
import type { RouteRecordRaw } from 'vue-router'
import { loadMessagePack, type Locale } from '@/i18n'
import { loadPrivacyMessages } from '@/i18n/privacy'
import { loadNotificationMessages } from '@/i18n/notifications'
import { loadAccountMessages } from '@/i18n/account'

const NotFound = () => import('@/components/NotFoundPage.vue')

/** Merges the agent message pack for the current locale (and `en`). */
export function loadAgentMessages(): Promise<void> {
  // The `privacy.*` (admin Privacy page, detail actions, "My data"; FE-080), `notifications.*`
  // and `account` (`mfa.*`, `mcp.*`) packs load with it.
  return Promise.all([
    loadMessagePack('agent', (l: Locale) => import(`../locales/agent/${l}.json`)),
    loadPrivacyMessages(),
    loadNotificationMessages(),
    loadAccountMessages(),
  ]).then(() => undefined)
}

const ADMIN = ['admin'] as const
const SUPPORT = ['admin', 'supervisor'] as const

type Meta = NonNullable<RouteRecordRaw['meta']>
const page = (name: string, path: string, component: RouteRecordRaw['component'], meta: Partial<Meta> = {}): RouteRecordRaw =>
  ({ path, name, component, meta: { area: 'agent', ...meta } }) as RouteRecordRaw

export const agentRoutes: RouteRecordRaw[] = [
  {
    path: '/agent/login',
    name: 'agent-login',
    component: () => import('@/areas/agent/AgentLoginPage.vue'),
    meta: { area: 'agent', guestOnly: true, title: 'agent.login.title' },
    beforeEnter: () => loadAgentMessages(),
  },
  {
    path: '/agent/no-access',
    name: 'agent-no-access',
    component: () => import('@/areas/agent/NoAccessPage.vue'),
    meta: { area: 'agent', public: true, title: 'agent.noAccess.title' },
    beforeEnter: () => loadAgentMessages(),
  },
  {
    path: '/agent',
    component: () => import('@/areas/agent/AgentLayout.vue'),
    meta: { area: 'agent' },
    beforeEnter: () => loadAgentMessages(),
    children: [
      { path: '', redirect: '/agent/tickets' },
      // Tickets (FE-021…FE-025, FE-038/039, FE-045/046, FE-054/055)
      page('agent-tickets', 'tickets', () => import('@/areas/agent/tickets/TicketListPage.vue'), { title: 'nav.tickets' }),
      page('agent-ticket-new', 'tickets/new', () => import('@/areas/agent/tickets/TicketNewPage.vue'), { title: 'nav.newTicket' }),
      page('agent-ticket-ref', 'tickets/:ref([A-Za-z][A-Za-z0-9]*-\\d+)', () => import('@/areas/agent/tickets/TicketDetailPage.vue'), { title: 'nav.tickets' }),
      page('agent-ticket', 'tickets/:number(\\d+)', () => import('@/areas/agent/tickets/TicketDetailPage.vue'), { title: 'nav.tickets' }),
      // Directory (FE-026, FE-030, FE-059 signature)
      page('agent-customers', 'customers', () => import('@/areas/agent/directory/CustomersPage.vue'), { title: 'nav.customers' }),
      page('agent-customer', 'customers/:id', () => import('@/areas/agent/directory/CustomerDetailPage.vue'), { title: 'nav.customers' }),
      page('agent-organizations', 'organizations', () => import('@/areas/agent/directory/OrganizationsPage.vue'), { title: 'nav.organizations' }),
      page('agent-organization', 'organizations/:id', () => import('@/areas/agent/directory/OrganizationDetailPage.vue'), { title: 'nav.organizations' }),
      page('agent-profile', 'profile', () => import('@/areas/agent/directory/ProfilePage.vue'), { title: 'nav.profile' }),
      page('agent-profile-tokens', 'profile/tokens', () => import('@/areas/agent/directory/TokensPage.vue'), { title: 'nav.apiTokens' }),
      // Notification centre (D-19 §D, FE-092)
      page('agent-notifications', 'notifications', () => import('@/notifications/NotificationsPage.vue'), { title: 'notifications.page.title' }),
      page('agent-profile-data', 'profile/data', () => import('@/areas/agent/privacy/MyDataPage.vue'), { title: 'privacy.nav.myData' }),
      // Knowledge base editor (FE-052) and reports (FE-043)
      page('agent-kb', 'kb', () => import('@/areas/agent/content/KbListPage.vue'), { title: 'nav.knowledgeBase' }),
      page('agent-kb-article-new', 'kb/articles/new', () => import('@/areas/agent/content/KbArticlePage.vue'), { title: 'nav.knowledgeBase' }),
      page('agent-kb-article', 'kb/articles/:id', () => import('@/areas/agent/content/KbArticlePage.vue'), { title: 'nav.knowledgeBase' }),
      page('agent-reports', 'reports', () => import('@/areas/agent/content/ReportsPage.vue'), { title: 'nav.reports' }),
      // Automation (D-19 A, FE-060…FE-067): personal macros for every agent
      page('agent-macros', 'macros', () => import('@/areas/agent/automation/MacrosPage.vue'), { title: 'automation.nav.myMacros' }),
      // Administration (role meta is UX only; the API enforces, permissions §6)
      page('admin-staff', 'admin/staff', () => import('@/areas/agent/directory/admin/StaffPage.vue'), { roles: [...ADMIN], title: 'agent.admin.nav.staff' }),
      page('admin-projects', 'admin/projects', () => import('@/areas/agent/projects/ProjectsPage.vue'), { roles: [...SUPPORT], orArea: 'project_admin', title: 'projects.nav.title' }),
      page('admin-teams', 'admin/teams', () => import('@/areas/agent/directory/admin/TeamsPage.vue'), { roles: [...SUPPORT], title: 'agent.admin.nav.teams' }),
      page('admin-categories', 'admin/categories', () => import('@/areas/agent/directory/admin/CategoriesPage.vue'), { roles: [...SUPPORT], orArea: 'project_admin', title: 'agent.admin.nav.categories' }),
      page('admin-tokens', 'admin/tokens', () => import('@/areas/agent/directory/admin/AllTokensPage.vue'), { roles: [...ADMIN], title: 'agent.admin.nav.tokens' }),
      page('admin-settings', 'admin/settings', () => import('@/areas/agent/settings/SupportSettingsPage.vue'), { roles: [...ADMIN], title: 'agent.admin.nav.settings' }),
      page('admin-system', 'admin/system', () => import('@/areas/agent/settings/SystemPage.vue'), { roles: [...ADMIN], title: 'agent.admin.nav.system' }),
      page('admin-queue', 'admin/queue', () => import('@/areas/agent/settings/QueuePage.vue'), { roles: [...ADMIN], title: 'agent.admin.nav.queue' }),
      page('admin-branding', 'admin/branding', () => import('@/areas/agent/settings/BrandingPage.vue'), { roles: [...ADMIN], title: 'agent.admin.nav.branding' }),
      page('admin-email', 'admin/email', () => import('@/areas/agent/settings/EmailPage.vue'), { roles: [...ADMIN], title: 'agent.admin.nav.email' }),
      page('admin-storage', 'admin/storage', () => import('@/areas/agent/settings/StoragePage.vue'), { roles: [...ADMIN], title: 'agent.admin.nav.storage' }),
      page('admin-audit', 'admin/audit', () => import('@/areas/agent/settings/AuditLogPage.vue'), { roles: [...ADMIN], title: 'agent.admin.nav.audit' }),
      page('admin-jobs', 'admin/jobs', () => import('@/areas/agent/settings/JobsPage.vue'), { roles: [...ADMIN], title: 'agent.admin.nav.jobs' }),
      page('admin-webhooks', 'admin/webhooks', () => import('@/areas/agent/config/WebhooksPage.vue'), { roles: [...ADMIN], title: 'agent.admin.nav.webhooks' }),
      page('admin-sla', 'admin/sla', () => import('@/areas/agent/config/SlaPage.vue'), { roles: [...SUPPORT], title: 'agent.admin.nav.sla' }),
      page('admin-canned', 'admin/canned', () => import('@/areas/agent/config/CannedPage.vue'), { roles: [...SUPPORT], orArea: 'project_admin', title: 'agent.admin.nav.canned' }),
      page('admin-tags', 'admin/tags', () => import('@/areas/agent/config/TagsPage.vue'), { roles: [...SUPPORT], title: 'agent.admin.nav.tags' }),
      page('admin-custom-fields', 'admin/custom-fields', () => import('@/areas/agent/config/CustomFieldsPage.vue'), { roles: [...ADMIN], title: 'agent.admin.nav.customFields' }),
      page('admin-calendars', 'admin/calendars', () => import('@/areas/agent/config/CalendarsPage.vue'), { roles: [...SUPPORT], title: 'agent.admin.nav.calendars' }),
      page('admin-kb-categories', 'admin/kb-categories', () => import('@/areas/agent/content/KbCategoriesPage.vue'), { roles: [...SUPPORT], orArea: 'project_admin', title: 'agent.admin.nav.kbCategories' }),
      page('admin-inbound', 'admin/inbound', () => import('@/areas/agent/content/InboundPage.vue'), { roles: [...ADMIN], title: 'agent.admin.nav.inbound' }),
      page('admin-inbound-log', 'admin/inbound/log', () => import('@/areas/agent/content/InboundLogPage.vue'), { roles: [...ADMIN], title: 'agent.admin.nav.inboundLog' }),
      // Privacy / GDPR (FE-080…FE-089, D-19 §C)
      page('admin-privacy', 'admin/privacy', () => import('@/areas/agent/privacy/PrivacyPage.vue'), { roles: [...ADMIN], title: 'privacy.nav.admin' }),
      page('admin-automation', 'admin/automation', () => import('@/areas/agent/automation/RulesPage.vue'), { roles: [...SUPPORT], orArea: 'project_admin', title: 'automation.nav.rules' }),
      { ...page('admin-macros', 'admin/macros', () => import('@/areas/agent/automation/MacrosPage.vue'), { roles: [...SUPPORT], orArea: 'project_admin', title: 'automation.nav.sharedMacros' }), props: { shared: true } } as RouteRecordRaw,
      { path: ':pathMatch(.*)*', name: 'agent-not-found', component: NotFound, meta: { area: 'agent' } },
    ],
  },
]

/** Admin navigation, in drawer order. Visibility comes from the route guard (`decide`). */
export const adminNav: { to: string; title: string; group: 'support' | 'people' | 'channels' | 'system' }[] = [
  { to: '/agent/admin/projects', title: 'projects.nav.title', group: 'people' },
  { to: '/agent/admin/teams', title: 'agent.admin.nav.teams', group: 'people' },
  { to: '/agent/admin/staff', title: 'agent.admin.nav.staff', group: 'people' },
  { to: '/agent/admin/privacy', title: 'privacy.nav.admin', group: 'people' },
  { to: '/agent/admin/categories', title: 'agent.admin.nav.categories', group: 'support' },
  { to: '/agent/admin/sla', title: 'agent.admin.nav.sla', group: 'support' },
  { to: '/agent/admin/calendars', title: 'agent.admin.nav.calendars', group: 'support' },
  { to: '/agent/admin/canned', title: 'agent.admin.nav.canned', group: 'support' },
  { to: '/agent/admin/tags', title: 'agent.admin.nav.tags', group: 'support' },
  { to: '/agent/admin/automation', title: 'automation.nav.rules', group: 'support' },
  { to: '/agent/admin/macros', title: 'automation.nav.sharedMacros', group: 'support' },
  { to: '/agent/admin/custom-fields', title: 'agent.admin.nav.customFields', group: 'support' },
  { to: '/agent/admin/kb-categories', title: 'agent.admin.nav.kbCategories', group: 'support' },
  { to: '/agent/admin/email', title: 'agent.admin.nav.email', group: 'channels' },
  { to: '/agent/admin/inbound', title: 'agent.admin.nav.inbound', group: 'channels' },
  { to: '/agent/admin/webhooks', title: 'agent.admin.nav.webhooks', group: 'channels' },
  { to: '/agent/admin/branding', title: 'agent.admin.nav.branding', group: 'system' },
  { to: '/agent/admin/settings', title: 'agent.admin.nav.settings', group: 'system' },
  { to: '/agent/admin/system', title: 'agent.admin.nav.system', group: 'system' },
  { to: '/agent/admin/queue', title: 'agent.admin.nav.queue', group: 'system' },
  { to: '/agent/admin/jobs', title: 'agent.admin.nav.jobs', group: 'system' },
  { to: '/agent/admin/storage', title: 'agent.admin.nav.storage', group: 'system' },
  { to: '/agent/admin/audit', title: 'agent.admin.nav.audit', group: 'system' },
  { to: '/agent/admin/tokens', title: 'agent.admin.nav.tokens', group: 'system' },
]
