// SPDX-License-Identifier: AGPL-3.0-only

/**
 * The page catalogue for the accessibility and responsive sweeps (FE-032):
 * one entry per named route of the SPA, with the identity that opens it and
 * the data it needs. `coverage()` reads the route files and returns every
 * route name that has no entry here, so a new page cannot slip past the scans.
 */
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import type { Browser, BrowserContextOptions, Page } from '@playwright/test'
import { expect, clientIp, ADMIN, type Credentials } from './fixtures'
import { apiFor, customerAccount, seedTeam, signIn, uid, type Person } from './agent/helpers'

export type Who = 'anon' | 'customer' | 'admin'

export interface Seed {
  id: string
  ticketId: string
  ticketNumber: number
  customerId: string
  organizationId: string
  kbCategorySlug: string
  kbArticleSlug: string
  kbArticleId: string
}

export interface PageSpec {
  who: Who
  path: (s: Seed) => string
  /** A locator that shows the page has rendered its content (default: the first heading). */
  ready?: string
}

/** Route name → how to open it; `skip` names a route that is not a page of its own. */
export const PAGES: Record<string, PageSpec | { skip: string }> = {
  // Portal, signed out
  'portal-login': { who: 'anon', path: () => '/login' },
  'portal-register': { who: 'anon', path: () => '/register' },
  'portal-verify': { who: 'anon', path: () => '/verify-email?token=not-a-real-token' },
  'portal-forgot': { who: 'anon', path: () => '/forgot-password' },
  'portal-reset': { who: 'anon', path: () => '/reset-password?token=not-a-real-token' },
  'portal-magic': { who: 'anon', path: () => '/magic-link?token=not-a-real-token' },
  'portal-invite': { who: 'anon', path: () => '/invite?token=not-a-real-token' },
  'portal-ticket-rate': { who: 'anon', path: (s) => `/tickets/${s.ticketId}/rate?score=4&token=not-a-real-token` },
  'not-found': { who: 'anon', path: () => '/no-such-page', ready: '.v-container' },
  // Portal, signed in (the customer is an organization manager, so every page is reachable)
  'portal-tickets': { who: 'customer', path: () => '/tickets' },
  'portal-ticket-new': { who: 'customer', path: () => '/tickets/new' },
  'portal-ticket': { who: 'customer', path: (s) => `/tickets/${s.ticketId}`, ready: '[data-testid="message-thread"]' },
  'portal-profile': { who: 'customer', path: () => '/profile' },
  'portal-tokens': { who: 'customer', path: () => '/profile/tokens' },
  'portal-sessions': { who: 'customer', path: () => '/profile/sessions' },
  'portal-security': { who: 'customer', path: () => '/profile/security', ready: '[data-testid="mfa-section"]' },
  'portal-org-members': { who: 'customer', path: () => '/organization/members' },
  // Public knowledge base
  'kb-home': { who: 'anon', path: () => '/kb' },
  'kb-category': { who: 'anon', path: (s) => `/kb/c/${s.kbCategorySlug}` },
  'kb-article': { who: 'anon', path: (s) => `/kb/a/${s.kbArticleSlug}`, ready: '[data-testid="kb-article-title"]' },
  // Agent panel, signed out
  'agent-login': { who: 'anon', path: () => '/agent/login', ready: '[data-testid="login-form"], [data-testid="sso-button"]' },
  'agent-no-access': { who: 'anon', path: () => '/agent/no-access', ready: '.v-container' },
  // Agent panel, as the admin (every page is reachable)
  'agent-tickets': { who: 'admin', path: () => '/agent/tickets' },
  'agent-ticket-new': { who: 'admin', path: () => '/agent/tickets/new' },
  'agent-ticket': { who: 'admin', path: (s) => `/agent/tickets/${s.ticketNumber}`, ready: '[data-testid="message-thread"]' },
  'agent-customers': { who: 'admin', path: () => '/agent/customers' },
  'agent-customer': { who: 'admin', path: (s) => `/agent/customers/${s.customerId}` },
  'agent-organizations': { who: 'admin', path: () => '/agent/organizations' },
  'agent-organization': { who: 'admin', path: (s) => `/agent/organizations/${s.organizationId}` },
  'agent-profile': { who: 'admin', path: () => '/agent/profile' },
  'agent-profile-tokens': { who: 'admin', path: () => '/agent/profile/tokens' },
  'agent-kb': { who: 'admin', path: () => '/agent/kb' },
  'agent-kb-article-new': { who: 'admin', path: () => '/agent/kb/articles/new', ready: '[data-testid="kb-article-form"]' },
  'agent-kb-article': { who: 'admin', path: (s) => `/agent/kb/articles/${s.kbArticleId}`, ready: '[data-testid="kb-article-form"]' },
  'agent-reports': { who: 'admin', path: () => '/agent/reports' },
  'admin-staff': { who: 'admin', path: () => '/agent/admin/staff' },
  'admin-teams': { who: 'admin', path: () => '/agent/admin/teams' },
  'admin-categories': { who: 'admin', path: () => '/agent/admin/categories' },
  'admin-tokens': { who: 'admin', path: () => '/agent/admin/tokens' },
  'admin-settings': { who: 'admin', path: () => '/agent/admin/settings' },
  'admin-system': { who: 'admin', path: () => '/agent/admin/system' },
  'admin-queue': { who: 'admin', path: () => '/agent/admin/queue' },
  'admin-branding': { who: 'admin', path: () => '/agent/admin/branding', ready: '[data-testid="branding-form"]' },
  'admin-email': { who: 'admin', path: () => '/agent/admin/email' },
  'admin-storage': { who: 'admin', path: () => '/agent/admin/storage' },
  'admin-audit': { who: 'admin', path: () => '/agent/admin/audit' },
  'admin-jobs': { who: 'admin', path: () => '/agent/admin/jobs' },
  'admin-webhooks': { who: 'admin', path: () => '/agent/admin/webhooks' },
  'admin-sla': { who: 'admin', path: () => '/agent/admin/sla' },
  'admin-canned': { who: 'admin', path: () => '/agent/admin/canned' },
  'admin-tags': { who: 'admin', path: () => '/agent/admin/tags' },
  'admin-custom-fields': { who: 'admin', path: () => '/agent/admin/custom-fields' },
  'admin-calendars': { who: 'admin', path: () => '/agent/admin/calendars' },
  'admin-kb-categories': { who: 'admin', path: () => '/agent/admin/kb-categories' },
  'admin-inbound': { who: 'admin', path: () => '/agent/admin/inbound' },
  'admin-inbound-log': { who: 'admin', path: () => '/agent/admin/inbound/log' },
  // Privacy / GDPR (D-19 §C, FE-082…FE-084)
  'portal-privacy': { who: 'customer', path: () => '/profile/data', ready: '[data-testid="my-data"]' },
  'agent-profile-data': { who: 'admin', path: () => '/agent/profile/data', ready: '[data-testid="my-data"]' },
  'admin-privacy': { who: 'admin', path: () => '/agent/admin/privacy' },
  // Notification centre (D-19 §D, FE-092)
  'portal-notifications': { who: 'customer', path: () => '/notifications', ready: '[data-testid="notifications-page"]' },
  'agent-notifications': { who: 'admin', path: () => '/agent/notifications', ready: '[data-testid="notifications-page"]' },
  // Automation (D-19 A, FE-062/FE-064)
  'agent-macros': { who: 'admin', path: () => '/agent/macros' },
  'admin-automation': { who: 'admin', path: () => '/agent/admin/automation' },
  'admin-macros': { who: 'admin', path: () => '/agent/admin/macros' },
  'agent-not-found': { who: 'admin', path: () => '/agent/no-such-page', ready: '.v-container' },
  // Standalone first-run wizard: only reachable on an instance without an admin; its steps are
  // scanned in portal/setup-wizard.spec.ts against such an instance (E2E_SETUP_BASE_URL).
  setup: { skip: 'first-run wizard, scanned per step in portal/setup-wizard.spec.ts' },
}

/** Every route name in the router sources that has no PAGES entry. */
export function coverage(): string[] {
  const read = (f: string) => readFileSync(fileURLToPath(new URL(`../src/router/${f}`, import.meta.url)), 'utf8')
  const names = new Set<string>()
  for (const src of [read('routes.ts'), read('agent-routes.ts')]) {
    for (const m of src.matchAll(/\bname:\s*'([\w-]+)'/g)) names.add(m[1]!)
    for (const m of src.matchAll(/\bpage\(\s*'([\w-]+)'/g)) names.add(m[1]!)
  }
  return [...names].filter((n) => !(n in PAGES)).sort()
}

export interface World {
  seed: Seed
  anon: Person
  customer: Person | null
  admin: Person
  close: () => Promise<void>
}

/** Seeds one of everything a page needs and signs in the three identities. */
/** `device` applies to the anonymous and customer contexts (e.g. the mobile project's device). */
export async function seedWorld(browser: Browser, page: Page, device: BrowserContextOptions = {}): Promise<World> {
  const id = uid()
  const team = await seedTeam(browser)
  const api = team.api
  const creds: Credentials | null = await customerAccount(page)

  const org = await api.post('/agent/organizations', { name: `A11y Org ${id}`, email_domains: [] })
  let customerId: string
  let requester: Record<string, string>
  if (creds) {
    const probe = await signIn(browser, 'customer', creds)
    customerId = (await (await probe.page.request.get('/api/v1/me')).json()).id
    await probe.context.close()
    // Organization manager: the members page is reachable too.
    await api.patch(`/agent/customers/${customerId}`, { organization_id: org.id, role: 'org_manager' })
    requester = { requester_id: customerId }
  } else {
    const c = await api.post('/agent/customers', { name: `A11y Customer ${id}`, email: `a11y-${id}@e2e.test` })
    customerId = c.id
    requester = { requester_id: c.id }
  }
  const ticket = await api.post(
    '/agent/tickets',
    { subject: `Accessibility check ${id}`, body_md: 'The **printer** is offline.', team_id: team.team.id, ...requester },
    { 'Idempotency-Key': `a11y-${id}` },
  )
  await api.post(`/agent/tickets/${ticket.id}/messages`, { kind: 'public', body_md: 'We are looking into it.' }, { 'Idempotency-Key': `a11y-m-${id}` })

  const cat = await api.post('/admin/kb/categories', { slug: `a11y-${id}`, title: { en: `Accessibility ${id}` }, visibility: 'public' })
  const art = await api.post('/agent/kb/articles', {
    category_id: cat.id,
    locale: 'en',
    title: `How to restart the printer ${id}`,
    body_md: '1. Switch it **off**.\n2. Wait ten seconds.\n3. Switch it on.',
    visibility: 'public',
  })
  await api.patch(`/agent/kb/articles/${art.id}`, { status: 'published' })

  const anonContext = await browser.newContext({ ...device, extraHTTPHeaders: clientIp() })
  const anon: Person = { context: anonContext, page: await anonContext.newPage(), creds: { email: '', password: '' } }
  const customer = creds ? await signIn(browser, 'customer', creds, device) : null
  const admin = await signIn(browser, 'staff', ADMIN)
  return {
    seed: {
      id,
      ticketId: ticket.id,
      ticketNumber: ticket.number,
      customerId,
      organizationId: org.id,
      kbCategorySlug: cat.slug,
      kbArticleSlug: art.slug,
      kbArticleId: art.id,
    },
    anon,
    customer,
    admin,
    close: async () => {
      await Promise.all([anonContext.close(), customer?.context.close(), admin.context.close(), team.admin.context.close()])
    },
  }
}

/** Opens a catalogue page and waits until it has rendered its content (not just the shell). */
export async function open(page: Page, spec: PageSpec, seed: Seed) {
  const res = await page.goto(spec.path(seed))
  expect(res?.ok() ?? true).toBe(true)
  await expect(page.locator(spec.ready ?? 'h1').first()).toBeVisible()
  // Loading indicators gone (list fetches, lazy chunks), then one settle for transitions.
  // (Determinate bars, e.g. queue fill levels, are content, not loading.)
  // An indeterminate progressbar has no aria-valuenow.
  await expect(page.locator('[role="progressbar"]:not([aria-valuenow]), .v-skeleton-loader, [aria-busy="true"]').filter({ visible: true })).toHaveCount(0)
  await page.waitForTimeout(300)
}

export { apiFor }
