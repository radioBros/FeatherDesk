// SPDX-License-Identifier: AGPL-3.0-only

/**
 * AUTH-023: staff SSO against a real Authentik (deploy/compose.dev.yml, blueprints
 * from deploy/authentik/). Runs only when E2E_AUTHENTIK_URL is set, against the
 * dev stack's app:
 *
 *   docker compose -f deploy/compose.yml -f deploy/compose.dev.yml up -d --build
 *   E2E_BASE_URL=http://localhost:8080 E2E_AUTHENTIK_URL=http://authentik.localhost:9000 \
 *     pnpm exec playwright test e2e/sso.spec.ts
 *
 * Every test creates its own Authentik user through the API (bootstrap token)
 * and deletes it afterwards, so the seeded dev users are never modified.
 * Each IdP login uses a fresh browser context: no Authentik session carries over.
 */
import type { Browser, Page } from '@playwright/test'
import { test, expect } from './fixtures'
import { AK, authentik, authentikSignIn, freshPage, ssoLogin } from './sso-helpers'

// Break-glass admin of the dev stack (deploy/.env.dev).
const ST_ADMIN = {
  email: process.env.E2E_ADMIN_EMAIL ?? 'admin@dev.test',
  password: process.env.E2E_ADMIN_PASSWORD ?? 'dev-admin-password-123',
}

/** The SimpleTickets staff record for an email, read by the break-glass admin. */
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

/** Runs `fn` with a page signed in as the break-glass admin (cookie + CSRF headers). */
async function asAdmin<T>(browser: Browser, fn: (page: Page, headers: Record<string, string>) => Promise<T>): Promise<T> {
  const { context, page } = await freshPage(browser)
  try {
    const login = await page.request.post('/api/v1/auth/login', { data: { audience: 'staff', ...ST_ADMIN } })
    expect(login.ok(), await login.text()).toBeTruthy()
    const me = (await login.json()) as { csrf_token: string }
    return await fn(page, { 'X-CSRF-Token': me.csrf_token, Origin: new URL(login.url()).origin })
  } finally {
    await context.close()
  }
}

// AUTH-031: `group=team` of the app's ST_OIDC_STAFF_TEAM_MAP (set by scripts/dev-team-map.sh).
const TEAM_MAP = process.env.E2E_TEAM_MAP ?? ''

test.describe('SSO with Authentik (AUTH-023)', () => {
  test.skip(!AK, 'needs E2E_AUTHENTIK_URL (deploy/compose.dev.yml with the blueprints applied)')
  // Every test is several full Authentik login rounds (create user, flow executor, OIDC callback,
  // delete): ~3 s each on a workstation, 10+ s on a shared 2-vCPU CI runner. Size the budget for that.
  test.describe.configure({ mode: 'serial', timeout: 180_000 })

  test('group → role and tier mapping on first login', async ({ browser, request }) => {
    const ak = authentik(request)
    const cases = [
      { slug: 'l2', groups: ['st-agents', 'st-l2'], role: 'agent', tier: 2 },
      { slug: 'sup', groups: ['st-supervisors', 'st-l3'], role: 'supervisor', tier: 3 },
      { slug: 'adm', groups: ['st-admins'], role: 'admin', tier: 1 },
    ]
    for (const c of cases) {
      const user = await ak.createUser(c.slug, c.groups)
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
        await ak.deleteUser(user)
      }
    }
  })

  test('removing the tier group demotes; removing the role group blocks and disables', async ({ browser, request }) => {
    const ak = authentik(request)
    const user = await ak.createUser('demote', ['st-agents', 'st-l2'])
    const unmapped = `e2e-unmapped-${Date.now().toString(36)}`
    let groupPk = ''
    let bindingPk = ''
    try {
      // 1. Tier 2 agent.
      let s = await freshPage(browser)
      await ssoLogin(s.page, user.username)
      expect(await (await s.page.request.get('/api/v1/me')).json()).toMatchObject({ role: 'agent', tier: 2 })
      await s.context.close()

      // 2. st-l2 removed at the IdP → tier 1 on the next login.
      await ak.setGroups(user, ['st-agents'])
      s = await freshPage(browser)
      await ssoLogin(s.page, user.username)
      expect(await (await s.page.request.get('/api/v1/me')).json()).toMatchObject({ role: 'agent', tier: 1 })
      await s.context.close()

      // 3. st-agents removed, but still allowed into the Authentik application through a
      //    group SimpleTickets does not map → SimpleTickets refuses and disables the user.
      const g = await ak.call('POST', '/core/groups/', { name: unmapped })
      groupPk = g.pk
      const app = await ak.call('GET', '/core/applications/simpletickets-staff/')
      const binding = await ak.call('POST', '/policies/bindings/', { target: app.pk, group: groupPk, order: 99, enabled: true, timeout: 30 })
      bindingPk = binding.pk
      await ak.setGroups(user, [unmapped])
      s = await freshPage(browser)
      await ssoLogin(s.page, user.username)
      await expect(s.page).toHaveURL(/\/agent\/login\?error=no_role/)
      await expect(s.page.getByTestId('login-error')).toContainText('no support role')
      expect((await s.page.request.get('/api/v1/me')).status()).toBe(401)
      await s.context.close()
      expect(await staffRecord(browser, user.email)).toMatchObject({ status: 'disabled' })

      // 4. Groups restored at the IdP: still blocked until an admin re-enables the account.
      await ak.setGroups(user, ['st-agents', 'st-l2'])
      s = await freshPage(browser)
      await ssoLogin(s.page, user.username)
      await expect(s.page).toHaveURL(/\/agent\/login\?error=disabled/)
      expect((await s.page.request.get('/api/v1/me')).status()).toBe(401)
      await s.context.close()

      // 5. In no st-* group at all: Authentik's application policy refuses before SimpleTickets.
      await ak.setGroups(user, [])
      s = await freshPage(browser)
      await s.page.goto('/agent/login')
      await s.page.getByTestId('sso-button').click()
      await authentikSignIn(s.page, user.username)
      await expect(s.page.getByText(/permission denied|access denied|not allowed/i).first()).toBeVisible({ timeout: 60_000 })
      expect(s.page.url().startsWith(AK)).toBe(true)
      await s.context.close()
    } finally {
      await ak.deleteUser(user)
      if (bindingPk) await ak.call('DELETE', `/policies/bindings/${bindingPk}/`)
      if (groupPk) await ak.call('DELETE', `/core/groups/${groupPk}/`)
    }
  })

  test('customer SSO login creates a customer, never staff (AUTH-025)', async ({ browser, request }) => {
    const ak = authentik(request)
    // Even an IdP user in staff groups gets only a customer account through the portal app.
    const user = await ak.createUser('cust', ['st-agents'])
    try {
      const { context, page } = await freshPage(browser)
      await page.goto('/login')
      await page.getByTestId('sso-button').click()
      await authentikSignIn(page, user.username)
      await page.waitForURL((u) => !u.href.startsWith(AK) && !u.pathname.includes('/auth/oidc/'), { timeout: 60_000 })
      await expect(page).toHaveURL(/\/tickets/)
      const me = await (await page.request.get('/api/v1/me')).json()
      expect(me).toMatchObject({ kind: 'customer', role: 'requester', email: user.email })
      await page.goto('/agent/tickets')
      await expect(page).not.toHaveURL(/\/agent\/tickets/)
      await context.close()
    } finally {
      await ak.deleteUser(user)
    }
  })

  test('RP-initiated logout goes through Authentik end-session and returns to /agent/login?logged_out=1', async ({ browser, request }) => {
    const ak = authentik(request)
    const user = await ak.createUser('logout', ['st-agents'])
    try {
      const { context, page } = await freshPage(browser)
      await ssoLogin(page, user.username)
      await expect(page.getByTestId('agent-drawer')).toBeVisible()

      const ended = page.waitForRequest((r) => r.url().startsWith(AK) && r.url().includes('end-session'))
      await page.getByTestId('user-menu').click()
      await page.getByTestId('sign-out').click()
      const endSession = new URL((await ended).url())
      expect(endSession.searchParams.get('id_token_hint')).toBeTruthy()
      expect(endSession.searchParams.get('post_logout_redirect_uri')).toMatch(/\/agent\/login\?logged_out=1$/)

      // Authentik accepts the hint and sends the browser back to SimpleTickets.
      // (Its default-provider-invalidation-flow logs out of this application only;
      // the Authentik session itself is the IdP's policy and is not asserted here.)
      await page.waitForURL(/\/agent\/login\?logged_out=1$/, { timeout: 60_000 })
      expect((await page.request.get('/api/v1/me')).status()).toBe(401)
      await expect(page.getByTestId('sso-button')).toBeVisible()
      await context.close()
    } finally {
      await ak.deleteUser(user)
    }
  })

  test('back-channel logout: signing out of Authentik revokes the SimpleTickets session (AUTH-030)', async ({ browser, request }) => {
    const ak = authentik(request)
    const user = await ak.createUser('bcl', ['st-agents'])
    try {
      const { context, page } = await freshPage(browser)
      await ssoLogin(page, user.username)
      await expect(page.getByTestId('agent-drawer')).toBeVisible()
      expect((await page.request.get('/api/v1/me')).status()).toBe(200)

      // Log out at the IdP only (Authentik's own invalidation flow, not SimpleTickets' sign-out).
      // Authentik deletes its session and its worker POSTs a logout_token (sub + sid) to
      // the provider's back-channel URI; nothing in the browser touches SimpleTickets.
      await page.goto(`${AK}/flows/-/default/invalidation/`)
      const started = Date.now()
      let status = 200
      while (Date.now() - started < 5_000) {
        status = (await page.request.get('/api/v1/me')).status()
        if (status === 401) break
        await page.waitForTimeout(100)
      }
      const elapsed = Date.now() - started
      console.log(`back-channel logout: /me 401 after ${elapsed} ms`)
      expect(status, 'next /me is 401 within 5 s of the Authentik logout').toBe(401)

      // The revocation is audited server-side.
      const audit = await asAdmin(browser, async (admin) => {
        const r = await admin.request.get('/api/v1/admin/audit-log?action=auth.backchannel_logout&limit=20')
        expect(r.ok(), await r.text()).toBeTruthy()
        return (await r.json()) as { items: { action: string; data?: Record<string, unknown> }[] }
      })
      expect(audit.items.some((e) => e.action === 'auth.backchannel_logout')).toBe(true)
      await context.close()
    } finally {
      await ak.deleteUser(user)
    }
  })

  test('team membership follows the IdP groups claim (AUTH-031)', async ({ browser, request }) => {
    test.skip(!TEAM_MAP, 'needs E2E_TEAM_MAP (the app booted with ST_OIDC_STAFF_TEAM_MAP, see scripts/dev-team-map.sh)')
    const [group, teamName] = TEAM_MAP.split('=')
    const ak = authentik(request)
    let createdGroup = ''
    const existing = await ak.call('GET', `/core/groups/?name=${encodeURIComponent(group)}`)
    if (existing.results.length === 0) createdGroup = (await ak.call('POST', '/core/groups/', { name: group })).pk
    const teamId = await asAdmin(browser, async (admin) => {
      const teams = (await (await admin.request.get('/api/v1/admin/teams')).json()) as { id: string; name: string }[]
      const t = teams.find((x) => x.name.toLowerCase() === teamName.toLowerCase())
      expect(t, `team ${teamName}`).toBeTruthy()
      return t!.id
    })
    const members = () =>
      asAdmin(browser, async (admin) => {
        const r = await admin.request.get(`/api/v1/admin/teams/${teamId}/members`)
        expect(r.ok(), await r.text()).toBeTruthy()
        return (await r.json()) as { user_id: string; source: string }[]
      })

    const user = await ak.createUser('team', ['st-agents', group])
    try {
      // 1. In the mapped group → member of the team, source idp.
      let s = await freshPage(browser)
      await ssoLogin(s.page, user.username)
      let me = await (await s.page.request.get('/api/v1/me')).json()
      expect(me.team_ids).toContain(teamId)
      expect((await members()).find((m) => m.user_id === me.id)).toMatchObject({ source: 'idp' })
      await s.context.close()

      // 2. Removed from the group at the IdP → no longer a member after the next login.
      await ak.setGroups(user, ['st-agents'])
      s = await freshPage(browser)
      await ssoLogin(s.page, user.username)
      me = await (await s.page.request.get('/api/v1/me')).json()
      expect(me.team_ids ?? []).not.toContain(teamId)
      expect((await members()).find((m) => m.user_id === me.id)).toBeUndefined()
      await s.context.close()

      // 3. Added back → member again.
      await ak.setGroups(user, ['st-agents', group])
      s = await freshPage(browser)
      await ssoLogin(s.page, user.username)
      me = await (await s.page.request.get('/api/v1/me')).json()
      expect(me.team_ids).toContain(teamId)
      await s.context.close()
    } finally {
      await ak.deleteUser(user)
      if (createdGroup) await ak.call('DELETE', `/core/groups/${createdGroup}/`)
    }
  })
})
