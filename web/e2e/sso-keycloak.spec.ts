// SPDX-License-Identifier: AGPL-3.0-only

/**
 * AUTH-029: the AUTH-023 staff scenarios against Keycloak instead of Authentik, with no
 * Authentik blueprint: only the realm import in deploy/keycloak/ (Group Membership
 * mapper, full group path off). Runs only when E2E_KEYCLOAK_URL is set:
 *
 *   docker compose -f deploy/compose.yml -f deploy/compose.keycloak.yml up -d --build --wait
 *   E2E_BASE_URL=http://localhost:8080 E2E_KEYCLOAK_URL=http://keycloak.localhost:8180 \
 *     pnpm exec playwright test e2e/sso-keycloak.spec.ts --workers=1
 *
 * Every test creates its own Keycloak user through the admin REST API and deletes it
 * afterwards. Each IdP login uses a fresh browser context (no Keycloak SSO session reuse).
 */
import type { APIRequestContext, Browser, Page } from '@playwright/test'
import { test, expect } from './fixtures'

const KC = process.env.E2E_KEYCLOAK_URL?.replace(/\/$/, '') ?? ''
const REALM = process.env.E2E_KEYCLOAK_REALM ?? 'simpletickets'
const KC_ADMIN = {
  username: process.env.E2E_KEYCLOAK_ADMIN ?? 'admin',
  password: process.env.E2E_KEYCLOAK_ADMIN_PASSWORD ?? 'dev-keycloak-admin-password',
}
// Break-glass admin of the dev stack (deploy/.env.dev).
const ST_ADMIN = {
  email: process.env.E2E_ADMIN_EMAIL ?? 'admin@dev.test',
  password: process.env.E2E_ADMIN_PASSWORD ?? 'dev-admin-password-123',
}
const PASSWORD = 'e2e-Sso-password-123!'

interface KcUser {
  id: string
  username: string
  email: string
}

/** Minimal Keycloak admin REST client (admin-cli password grant on the master realm). */
function keycloak(request: APIRequestContext) {
  const token = async () => {
    const res = await request.post(`${KC}/realms/master/protocol/openid-connect/token`, {
      form: { grant_type: 'password', client_id: 'admin-cli', ...KC_ADMIN },
    })
    expect(res.ok(), `keycloak admin token: ${res.status()} ${await res.text()}`).toBeTruthy()
    return ((await res.json()) as { access_token: string }).access_token
  }
  const call = async (method: 'GET' | 'POST' | 'PUT' | 'DELETE', path: string, data?: unknown) => {
    const res = await request.fetch(`${KC}/admin/realms/${REALM}${path}`, {
      method,
      headers: { Authorization: `Bearer ${await token()}` },
      data,
    })
    expect(res.ok(), `keycloak ${method} ${path}: ${res.status()} ${await res.text()}`).toBeTruthy()
    return res
  }
  const groupId = async (name: string): Promise<string> => {
    const groups = (await (await call('GET', `/groups?search=${encodeURIComponent(name)}&exact=true`)).json()) as { id: string; name: string }[]
    const g = groups.find((x) => x.name === name)
    expect(g, `group ${name}`).toBeTruthy()
    return g!.id
  }
  return {
    async createUser(slug: string, groups: string[]): Promise<KcUser> {
      const username = `e2e-${slug}-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 5)}`
      const email = `${username}@dev.test`
      const res = await call('POST', '/users', {
        username,
        email,
        emailVerified: true,
        enabled: true,
        firstName: 'E2E',
        lastName: slug,
        credentials: [{ type: 'password', value: PASSWORD, temporary: false }],
        groups: groups.map((g) => `/${g}`),
      })
      const id = res.headers()['location']?.split('/').pop() ?? ''
      expect(id, 'created user id').toBeTruthy()
      return { id, username, email }
    },
    async setGroups(u: KcUser, groups: string[]) {
      const have = (await (await call('GET', `/users/${u.id}/groups`)).json()) as { id: string; name: string }[]
      for (const g of have) if (!groups.includes(g.name)) await call('DELETE', `/users/${u.id}/groups/${g.id}`)
      for (const name of groups) if (!have.some((g) => g.name === name)) await call('PUT', `/users/${u.id}/groups/${await groupId(name)}`)
    },
    /** Ends every Keycloak session of the user (admin "Sign out"): triggers back-channel logout. */
    async logoutUser(u: KcUser) {
      await call('POST', `/users/${u.id}/logout`)
    },
    async deleteUser(u: KcUser) {
      await call('DELETE', `/users/${u.id}`)
    },
  }
}

/** Fills Keycloak's login form until the browser leaves the IdP. */
async function keycloakSignIn(page: Page, username: string) {
  await page.waitForURL((u) => u.href.startsWith(KC))
  await page.locator('#username').fill(username)
  await page.locator('#password').fill(PASSWORD)
  await page.locator('#kc-login').click()
}

async function ssoLogin(page: Page, username: string, path = '/agent/login') {
  await page.goto(path)
  await page.getByTestId('sso-button').click()
  await keycloakSignIn(page, username)
  await page.waitForURL((u) => !u.href.startsWith(KC) && !u.pathname.includes('/auth/oidc/'), { timeout: 60_000 })
}

async function freshPage(browser: Browser) {
  const context = await browser.newContext()
  return { context, page: await context.newPage() }
}

async function staffRecord(browser: Browser, email: string) {
  const { context, page } = await freshPage(browser)
  const login = await page.request.post('/api/v1/auth/login', { data: { audience: 'staff', ...ST_ADMIN } })
  expect(login.ok(), await login.text()).toBeTruthy()
  const res = await page.request.get(`/api/v1/admin/staff?q=${encodeURIComponent(email)}&limit=5`)
  expect(res.ok(), await res.text()).toBeTruthy()
  const { items } = (await res.json()) as { items: { email: string; status: string; role: string; tier: number | null }[] }
  await context.close()
  return items.find((u) => u.email === email) ?? null
}

test.describe('SSO with Keycloak (AUTH-029)', () => {
  test.skip(!KC, 'needs E2E_KEYCLOAK_URL (deploy/compose.keycloak.yml)')
  test.describe.configure({ mode: 'serial', timeout: 180_000 })

  test('seeded realm user: groups claim without full path maps role and tier', async ({ browser }) => {
    const { context, page } = await freshPage(browser)
    await page.goto('/agent/login')
    await page.getByTestId('sso-button').click()
    await page.waitForURL((u) => u.href.startsWith(KC))
    await page.locator('#username').fill('kc-agent-l2')
    await page.locator('#password').fill('dev-Password-123!')
    await page.locator('#kc-login').click()
    await expect(page).toHaveURL(/\/agent\/tickets/, { timeout: 60_000 })
    expect(await (await page.request.get('/api/v1/me')).json()).toMatchObject({ kind: 'staff', role: 'agent', tier: 2, idp_managed: true })
    await context.close()
  })

  test('group → role and tier mapping on first login', async ({ browser, request }) => {
    const kc = keycloak(request)
    const cases = [
      { slug: 'l2', groups: ['st-agents', 'st-l2'], role: 'agent', tier: 2 },
      { slug: 'sup', groups: ['st-supervisors', 'st-l3'], role: 'supervisor', tier: 3 },
      { slug: 'adm', groups: ['st-admins'], role: 'admin', tier: 1 },
    ]
    for (const c of cases) {
      const user = await kc.createUser(c.slug, c.groups)
      try {
        const { context, page } = await freshPage(browser)
        await ssoLogin(page, user.username)
        await expect(page).toHaveURL(/\/agent\/tickets/)
        await expect(page.getByTestId('agent-drawer')).toBeVisible()
        const me = await (await page.request.get('/api/v1/me')).json()
        expect(me, c.slug).toMatchObject({ kind: 'staff', email: user.email, role: c.role, idp_managed: true })
        expect(me.tier ?? me.effective_tier, c.slug).toBe(c.tier)
        await context.close()
      } finally {
        await kc.deleteUser(user)
      }
    }
  })

  test('removing the tier group demotes; removing the role group blocks and disables', async ({ browser, request }) => {
    const kc = keycloak(request)
    const user = await kc.createUser('demote', ['st-agents', 'st-l2'])
    try {
      let s = await freshPage(browser)
      await ssoLogin(s.page, user.username)
      expect(await (await s.page.request.get('/api/v1/me')).json()).toMatchObject({ role: 'agent', tier: 2 })
      await s.context.close()

      await kc.setGroups(user, ['st-agents'])
      s = await freshPage(browser)
      await ssoLogin(s.page, user.username)
      expect(await (await s.page.request.get('/api/v1/me')).json()).toMatchObject({ role: 'agent', tier: 1 })
      await s.context.close()

      // No st-* group: Keycloak has no per-application policy here, so SimpleTickets itself refuses.
      await kc.setGroups(user, [])
      s = await freshPage(browser)
      await ssoLogin(s.page, user.username)
      await expect(s.page).toHaveURL(/\/agent\/login\?error=no_role/)
      expect((await s.page.request.get('/api/v1/me')).status()).toBe(401)
      await s.context.close()
      expect(await staffRecord(browser, user.email)).toMatchObject({ status: 'disabled' })

      // Groups restored: still blocked until an admin re-enables the account.
      await kc.setGroups(user, ['st-agents', 'st-l2'])
      s = await freshPage(browser)
      await ssoLogin(s.page, user.username)
      await expect(s.page).toHaveURL(/\/agent\/login\?error=disabled/)
      expect((await s.page.request.get('/api/v1/me')).status()).toBe(401)
      await s.context.close()
    } finally {
      await kc.deleteUser(user)
    }
  })

  test('customer SSO login creates a customer, never staff', async ({ browser, request }) => {
    const kc = keycloak(request)
    const user = await kc.createUser('cust', ['st-agents'])
    try {
      const { context, page } = await freshPage(browser)
      await ssoLogin(page, user.username, '/login')
      await expect(page).toHaveURL(/\/tickets/)
      expect(await (await page.request.get('/api/v1/me')).json()).toMatchObject({ kind: 'customer', role: 'requester', email: user.email })
      await context.close()
    } finally {
      await kc.deleteUser(user)
    }
  })

  test('RP-initiated logout goes through Keycloak end-session and returns to /agent/login?logged_out=1', async ({ browser, request }) => {
    const kc = keycloak(request)
    const user = await kc.createUser('logout', ['st-agents'])
    try {
      const { context, page } = await freshPage(browser)
      await ssoLogin(page, user.username)
      await expect(page.getByTestId('agent-drawer')).toBeVisible()
      const ended = page.waitForRequest((r) => r.url().startsWith(KC) && r.url().includes('/protocol/openid-connect/logout'))
      await page.getByTestId('user-menu').click()
      await page.getByTestId('sign-out').click()
      const endSession = new URL((await ended).url())
      expect(endSession.searchParams.get('id_token_hint')).toBeTruthy()
      expect(endSession.searchParams.get('post_logout_redirect_uri')).toMatch(/\/agent\/login\?logged_out=1$/)
      await page.waitForURL(/\/agent\/login\?logged_out=1$/, { timeout: 60_000 })
      expect((await page.request.get('/api/v1/me')).status()).toBe(401)
      await context.close()
    } finally {
      await kc.deleteUser(user)
    }
  })

  test('back-channel logout: ending the Keycloak session revokes the SimpleTickets session', async ({ browser, request }) => {
    const kc = keycloak(request)
    const user = await kc.createUser('bcl', ['st-agents'])
    try {
      const { context, page } = await freshPage(browser)
      await ssoLogin(page, user.username)
      expect((await page.request.get('/api/v1/me')).status()).toBe(200)
      await kc.logoutUser(user)
      const started = Date.now()
      let status = 200
      while (Date.now() - started < 5_000) {
        status = (await page.request.get('/api/v1/me')).status()
        if (status === 401) break
        await page.waitForTimeout(100)
      }
      console.log(`keycloak back-channel logout: /me 401 after ${Date.now() - started} ms`)
      expect(status, 'next /me is 401 within 5 s of the Keycloak logout').toBe(401)
      await context.close()
    } finally {
      await kc.deleteUser(user)
    }
  })
})
