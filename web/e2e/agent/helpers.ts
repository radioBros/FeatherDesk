// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Agent E2E helpers: seed staff, teams and tickets through the admin API
 * (cookie session + CSRF), each person in their own browser context.
 */
import type { APIRequestContext, Browser, BrowserContext, BrowserContextOptions, Page } from '@playwright/test'
import { expect, loginAs, clientIp, ADMIN, type Credentials } from '../fixtures'

export const uid = () => `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`

/** An authenticated API client for a signed-in page (sends the CSRF header on mutations). */
export async function apiFor(page: Page) {
  const meRes = await page.request.get('/api/v1/me')
  const me = await meRes.json()
  // Cookie-authenticated mutations need the CSRF token and a same-origin Origin header.
  const headers = { 'X-CSRF-Token': me.csrf_token as string, Origin: new URL(meRes.url()).origin }
  const req: APIRequestContext = page.request
  const ok = async (p: Promise<import('@playwright/test').APIResponse>) => {
    const r = await p
    expect(r.ok(), `${r.url()} → ${r.status()} ${await r.text()}`).toBeTruthy()
    return r.status() === 204 ? null : r.json()
  }
  return {
    me,
    get: (path: string) => ok(req.get('/api/v1' + path)),
    post: (path: string, data: unknown, extra: Record<string, string> = {}) => ok(req.post('/api/v1' + path, { data, headers: { ...headers, ...extra } })),
    patch: (path: string, data: unknown) => ok(req.patch('/api/v1' + path, { data, headers })),
    put: (path: string, data: unknown) => ok(req.put('/api/v1' + path, { data, headers })),
  }
}

export interface Person {
  context: BrowserContext
  page: Page
  creds: Credentials
}

export async function signIn(browser: Browser, audience: 'staff' | 'customer', creds: Credentials, options: BrowserContextOptions = {}): Promise<Person> {
  // Its own client IP: the per-IP login throttle (AUTH-012) applies per person.
  const context = await browser.newContext({ ...options, extraHTTPHeaders: { ...options.extraHTTPHeaders, ...clientIp() } })
  const page = await context.newPage()
  await loginAs(page, audience, creds)
  return { context, page, creds }
}

/** Admin seeds a team with an L1 and an L2 agent (local passwords; FD_AUTH_STAFF_LOCAL=true in the E2E server). */
export async function seedTeam(browser: Browser) {
  const admin = await signIn(browser, 'staff', ADMIN)
  const api = await apiFor(admin.page)
  const id = uid()
  const team = await api.post('/admin/teams', { name: `E2E ${id}`, notify_mode: 'agents' })
  const password = `e2e-agent-password-${id}`
  const mk = async (tier: number) => {
    const email = `l${tier}-${id}@e2e.test`
    await api.post('/admin/staff', { email, name: `L${tier} Agent ${id}`, role: 'agent', tier, team_ids: [team.id], password })
    return { email, password }
  }
  return { admin, api, team, l1: await mk(1), l2: await mk(2), id }
}

/**
 * A customer that can sign in to the portal. Uses E2E_CUSTOMER_EMAIL /
 * E2E_CUSTOMER_PASSWORD when set; otherwise registers one and confirms it
 * through Mailpit (E2E_MAILPIT_URL). Returns null when neither is available.
 */
export async function customerAccount(page: Page): Promise<Credentials | null> {
  if (process.env.E2E_CUSTOMER_EMAIL && process.env.E2E_CUSTOMER_PASSWORD)
    return { email: process.env.E2E_CUSTOMER_EMAIL, password: process.env.E2E_CUSTOMER_PASSWORD }
  const mailpit = process.env.E2E_MAILPIT_URL
  if (!mailpit) return null
  const email = `cust-${uid()}@e2e.test`
  const password = `e2e-customer-password-${uid()}`
  const reg = await page.request.post('/api/v1/auth/register', { data: { email, name: 'E2E Customer', password, locale: 'en' } })
  if (!reg.ok()) return null
  let token: string | null = null
  for (let i = 0; i < 20 && !token; i++) {
    const list = await (await page.request.get(`${mailpit}/api/v1/search?query=${encodeURIComponent('to:' + email)}`)).json()
    const id = list.messages?.[0]?.ID
    if (id) {
      const msg = await (await page.request.get(`${mailpit}/api/v1/message/${id}`)).json()
      token = /verify-email\?token=([\w-]+)/.exec(String(msg.Text ?? msg.HTML ?? ''))?.[1] ?? null
    }
    if (!token) await page.waitForTimeout(500)
  }
  if (!token) return null
  const v = await page.request.post('/api/v1/auth/verify-email', { data: { token } })
  return v.ok() ? { email, password } : null
}
