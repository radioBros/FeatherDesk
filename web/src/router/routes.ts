// SPDX-License-Identifier: AGPL-3.0-only

import type { RouteRecordRaw } from 'vue-router'
import { agentRoutes } from './agent-routes'
import { useConfigStore } from '@/stores/config'
import { requireEmail, requireRegistration, withKbMessages, withPortalMessages, withSetupMessages } from '@/areas/portal/routeGuards'

const NotFound = () => import('@/components/NotFoundPage.vue')
// Portal, KB and setup pages: one lazy chunk per page under their own area folders.
const portalPages = import.meta.glob('../areas/portal/pages/*.vue')
const kbPages = import.meta.glob('../areas/kb/pages/*.vue')
const portal = (name: string) => portalPages[`../areas/portal/pages/${name}.vue`]!
const kb = (name: string) => kbPages[`../areas/kb/pages/${name}.vue`]!

/**
 * Areas are separate lazy chunks: customers never download the agent panel
 * (FE-008, FE-031 budget).
 */
export const portalRoutes: RouteRecordRaw[] = [
  {
    path: '/',
    component: () => import('@/areas/portal/PortalLayout.vue'),
    meta: { area: 'portal' },
    beforeEnter: withPortalMessages,
    children: [
      // Portal off: the root is the staff panel.
      { path: '', redirect: () => (useConfigStore().config.portal_enabled === false ? '/agent' : '/tickets') },
      { path: 'login', name: 'portal-login', component: () => import('@/areas/portal/PortalLoginPage.vue'), meta: { area: 'portal', guestOnly: true } },
      { path: 'register', name: 'portal-register', component: portal('RegisterPage'), beforeEnter: requireRegistration, meta: { area: 'portal', guestOnly: true, title: 'auth.register' } },
      { path: 'verify-email', name: 'portal-verify', component: portal('VerifyEmailPage'), meta: { area: 'portal', public: true, title: 'portal.account.verifyTitle' } },
      { path: 'forgot-password', name: 'portal-forgot', component: portal('ForgotPasswordPage'), beforeEnter: requireEmail, meta: { area: 'portal', guestOnly: true, title: 'portal.account.forgotTitle' } },
      { path: 'reset-password', name: 'portal-reset', component: portal('ResetPasswordPage'), meta: { area: 'portal', public: true, title: 'portal.account.resetTitle' } },
      { path: 'magic-link', name: 'portal-magic', component: portal('MagicLinkPage'), meta: { area: 'portal', public: true, title: 'portal.account.magicTitle' } },
      { path: 'invite', name: 'portal-invite', component: portal('InvitePage'), meta: { area: 'portal', public: true, title: 'portal.account.inviteTitle' } },
      { path: 'tickets', name: 'portal-tickets', component: portal('TicketListPage'), meta: { area: 'portal', title: 'nav.myTickets' } },
      { path: 'tickets/new', name: 'portal-ticket-new', component: portal('NewTicketPage'), meta: { area: 'portal', title: 'nav.newTicket' } },
      { path: 'tickets/:id', name: 'portal-ticket', component: portal('TicketDetailPage'), meta: { area: 'portal' } },
      // Public: the emailed one-click rating link works signed out with its csat_token (FE-036).
      { path: 'tickets/:id/rate', name: 'portal-ticket-rate', component: portal('CsatPage'), meta: { area: 'portal', public: true, title: 'portal.csat.title' } },
      { path: 'profile', name: 'portal-profile', component: portal('ProfilePage'), meta: { area: 'portal', title: 'nav.profile' } },
      { path: 'profile/tokens', name: 'portal-tokens', component: portal('TokensPage'), meta: { area: 'portal', title: 'nav.apiTokens' } },
      { path: 'profile/security', name: 'portal-security', component: portal('SecurityPage'), meta: { area: 'portal', title: 'mfa.profile.title' } },
      { path: 'profile/sessions', name: 'portal-sessions', component: portal('SessionsPage'), meta: { area: 'portal', title: 'portal.sessions.title' } },
      { path: 'notifications', name: 'portal-notifications', component: () => import('@/notifications/NotificationsPage.vue'), meta: { area: 'portal', title: 'notifications.page.title' } },
      { path: 'profile/data', name: 'portal-privacy', component: portal('MyDataPage'), meta: { area: 'portal', title: 'privacy.nav.myData' } },
      { path: 'organization/members', name: 'portal-org-members', component: portal('OrgMembersPage'), meta: { area: 'portal', roles: ['org_manager'], title: 'portal.members.title' } },
    ],
  },
]

// The agent panel's routes live in their own file (lazy pages + lazy message pack).
export { agentRoutes } from './agent-routes'

export const kbRoutes: RouteRecordRaw[] = [
  {
    path: '/kb',
    component: () => import('@/areas/kb/KbLayout.vue'),
    beforeEnter: withKbMessages,
    meta: { area: 'kb', public: true },
    children: [
      { path: '', name: 'kb-home', component: kb('KbHomePage'), meta: { area: 'kb', public: true, title: 'nav.help' } },
      { path: 'c/:slug', name: 'kb-category', component: kb('KbCategoryPage'), meta: { area: 'kb', public: true } },
      { path: 'a/:slug', name: 'kb-article', component: kb('KbArticlePage'), meta: { area: 'kb', public: true } },
    ],
  },
]

export const setupRoutes: RouteRecordRaw[] = [
  {
    path: '/setup',
    name: 'setup',
    component: () => import('@/areas/setup/SetupPage.vue'),
    meta: { area: 'setup', public: true, title: 'setup.title' },
    beforeEnter: withSetupMessages,
  },
]

export const routes: RouteRecordRaw[] = [
  ...setupRoutes,
  ...kbRoutes,
  ...agentRoutes,
  ...portalRoutes,
  { path: '/:pathMatch(.*)*', name: 'not-found', component: NotFound, meta: { area: 'portal', public: true } },
]
