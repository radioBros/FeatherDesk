// SPDX-License-Identifier: AGPL-3.0-only

/**
 * DEP-022 end-to-end smoke of the shipped stack: the prod compose + the dev
 * overlay (app image built from source, Authentik with the blueprints, Mailpit)
 * on a FRESH, empty database with no break-glass admin:
 *
 *   setup wizard → staff SSO login (Authentik) → customer registers (link from
 *   Mailpit) → opens a ticket → the agent's reply appears live in the portal
 *   over the WebSocket → escalate → resolve (live) → the reply mail is in Mailpit.
 *
 *   FD_DEV_BOOTSTRAP_ADMIN_EMAIL= FD_DEV_BOOTSTRAP_ADMIN_PASSWORD= \
 *     docker compose -f deploy/compose.yml -f deploy/compose.dev.yml up -d --build
 *   tok=$(docker compose -f deploy/compose.yml -f deploy/compose.dev.yml logs app | grep -oE 'setup token: [A-Za-z0-9_-]+' | tail -1 | cut -d' ' -f3)
 *   E2E_BASE_URL=http://localhost:8080 E2E_AUTHENTIK_URL=http://authentik.localhost:9000 \
 *     E2E_MAILPIT_URL=http://localhost:8025 E2E_SETUP_TOKEN=$tok \
 *     pnpm exec playwright test e2e/smoke-stack.spec.ts --project=chromium
 *
 * One-shot: the wizard completes once per instance, so it runs in chromium only
 * and never retries (a retry would meet a closed /setup and hide the real error).
 */
import type { Page } from '@playwright/test'
import { test, expect, watchConsole } from './fixtures'
import { uid } from './agent/helpers'
import { MAILPIT, findInMail, linkFromMail, pathOf, uniqueEmail } from './portal/helpers'
import { AK, authentik, freshPage, ssoLogin } from './sso-helpers'

const SETUP_TOKEN = process.env.E2E_SETUP_TOKEN ?? ''

/** Logs how long each phase took, so the CI log shows where the smoke spends its budget. */
async function phase<T>(name: string, fn: () => Promise<T>): Promise<T> {
  const t0 = Date.now()
  const out = await test.step(name, fn)
  console.log(`[smoke] ${name}: ${Date.now() - t0} ms`)
  return out
}

/** Marks the page; the marker survives only as long as the page never reloads. */
const markAlive = (p: Page) => p.evaluate(() => ((window as unknown as { __smoke: string }).__smoke = 'alive'))
const isAlive = (p: Page) => p.evaluate(() => (window as unknown as { __smoke?: string }).__smoke === 'alive')

test.describe('stack smoke: prod compose + dev overlay + SSO (DEP-022)', () => {
  test.skip(!SETUP_TOKEN || !AK || !MAILPIT, 'needs E2E_SETUP_TOKEN (fresh instance), E2E_AUTHENTIK_URL and E2E_MAILPIT_URL: see the header')
  test.describe.configure({ retries: 0 })

  test('setup wizard → staff SSO → register → ticket → live reply → escalate → resolve → mail', async ({ page, browser, request }, info) => {
    test.skip(info.project.name !== 'chromium', 'one-shot: the setup wizard completes once per instance')
    test.setTimeout(300_000)
    const id = uid()

    // 1. First-run wizard on the empty database: token → admin → branding → SMTP (env-locked
    //    to Mailpit, skipped) → SSO hints → done, signed in as the new admin.
    await phase('setup wizard', async () => {
      const problems = watchConsole(page)
      await page.goto('/setup')
      await expect(page.getByTestId('setup-stepper')).toBeVisible()
      await page.getByTestId('setup-token').locator('input').fill(SETUP_TOKEN)
      await page.getByTestId('setup-token-form').getByTestId('setup-next').click()
      await page.getByTestId('setup-admin-name').locator('input').fill('Smoke Admin')
      await page.getByTestId('setup-admin-email').locator('input').fill(`smoke-admin-${id}@dev.test`)
      await page.getByTestId('setup-admin-password').locator('input').fill(`smoke-admin-password-${id}`)
      await page.getByTestId('setup-admin-confirm').locator('input').fill(`smoke-admin-password-${id}`)
      await page.getByTestId('setup-admin-form').getByTestId('setup-next').click()
      await page.getByTestId('setup-product-name').locator('input').fill('Smoke Desk')
      await page.getByTestId('setup-branding-form').getByTestId('setup-next').click()
      await page.getByTestId('smtp-skip').click()
      await expect(page.getByTestId('setup-sso')).toContainText('/api/v1/auth/oidc/staff/callback')
      await page.getByTestId('setup-sso').getByTestId('setup-next').click()
      await page.getByTestId('setup-finish').click()
      await expect(page).toHaveURL(/\/agent/)
      expect(await (await page.request.get('/api/v1/me')).json()).toMatchObject({ kind: 'staff', role: 'admin' })
      expect(problems).toEqual([])
    })

    // 2. Staff SSO: an L2 agent created at the IdP signs in through Authentik (JIT account,
    //    role and tier from the groups claim). Tier 2 keeps the ticket visible after escalation.
    const ak = authentik(request)
    const idpUser = await ak.createUser('smoke', ['fd-agents', 'fd-l2'])
    const agent = await freshPage(browser)
    try {
      await phase('staff SSO login', async () => {
        await ssoLogin(agent.page, idpUser.username)
        await expect(agent.page).toHaveURL(/\/agent\/tickets/)
        expect(await (await agent.page.request.get('/api/v1/me')).json()).toMatchObject({ kind: 'staff', role: 'agent', tier: 2, idp_managed: true, email: idpUser.email })
      })

      // 3. A customer registers, verifies through the link mailed to Mailpit, signs in and opens a ticket.
      const customer = await freshPage(browser)
      const frames: string[] = []
      customer.page.on('websocket', (ws) => ws.on('framereceived', (f) => frames.push(typeof f.payload === 'string' ? f.payload : f.payload.toString('utf8'))))
      const email = uniqueEmail(info, 'smoke')
      const password = `smoke-customer-password-${id}`
      const subject = `Smoke printer on fire ${id}`
      const ticketRef = await phase('customer register → verify → ticket', async () => {
        const p = customer.page
        await p.goto('/register')
        await p.getByTestId('register-name').locator('input').fill('Smoke Customer')
        await p.getByTestId('register-email').locator('input').fill(email)
        await p.getByTestId('register-password').locator('input').fill(password)
        await p.getByTestId('register-confirm').locator('input').fill(password)
        await p.getByTestId('register-submit').click()
        await expect(p.getByTestId('register-done')).toContainText(email)
        const link = await linkFromMail(p.request, email, /https?:\/\/[^\s"'<>()]+\/verify-email\?token=[\w-]+/)
        await p.goto(pathOf(link))
        await expect(p.getByTestId('verify-done')).toBeVisible()
        if (!(await p.request.get('/api/v1/me')).ok()) {
          await p.goto('/login')
          await p.getByTestId('login-email').locator('input').fill(email)
          await p.getByTestId('login-password').locator('input').fill(password)
          await p.getByTestId('login-submit').click()
        } else {
          await p.goto('/tickets')
        }
        await expect(p).toHaveURL(/\/tickets$/)
        await p.goto('/tickets/new')
        await p.getByTestId('nt-subject').locator('input').fill(subject)
        await p.getByTestId('md-input').locator('textarea').fill(`CUSTOMER-${id} The 2nd floor printer smokes.`)
        await p.getByTestId('nt-submit').click()
        // The detail page is addressed by the ticket's project reference (D-20), e.g. HD-1001.
        await expect(p).toHaveURL(/\/tickets\/[A-Z][A-Z0-9]*-\d+$/)
        await expect(p.getByTestId('detail-subject')).toContainText(subject)
        await markAlive(p)
        return p.url().split('/').pop()!
      })

      // 4. The SSO agent answers; the customer's open page receives it over the WebSocket.
      const reply = `PUBLIC-${id} We are sending a technician.`
      await phase('agent reply → live in the portal', async () => {
        await agent.page.goto(`/agent/tickets/${ticketRef}`)
        await expect(agent.page.getByTestId('ticket-subject')).toHaveText(subject)
        await expect(agent.page.getByTestId('message-thread')).toContainText(`CUSTOMER-${id}`)
        await agent.page.getByTestId('composer-body').locator('textarea').fill(reply)
        await agent.page.getByTestId('composer-send').click()
        await expect(agent.page.getByTestId('message-thread')).toContainText(reply)
        await expect(customer.page.getByTestId('message-thread')).toContainText(reply)
        expect(await isAlive(customer.page)).toBe(true)
      })

      // 5. Escalate L1 → L2 with a reason; the tier-2 agent keeps the ticket.
      await phase('escalate', async () => {
        await agent.page.getByTestId('escalate-open').click()
        await agent.page.getByTestId('escalate-reason').locator('textarea').fill(`Needs a specialist ${id}`)
        await agent.page.getByTestId('escalate-submit').click()
        await expect(agent.page.getByTestId('ticket-header').getByTestId('tier-chip')).toHaveText('L2')
        await agent.page.getByTestId('tab-timeline').click()
        await expect(agent.page.getByTestId('timeline')).toContainText(`Needs a specialist ${id}`)
        await agent.page.getByTestId('tab-thread').click()
      })

      // 6. Answer and resolve in one step; the portal shows the answer and Resolved live.
      const fix = `RESOLVED-${id} The technician replaced the fuser.`
      await phase('resolve → live in the portal', async () => {
        await agent.page.getByTestId('composer-body').locator('textarea').fill(fix)
        await agent.page.getByTestId('composer-send-menu').click()
        await agent.page.getByTestId('send-status-resolved').click()
        await expect(agent.page.getByTestId('ticket-header').getByTestId('status-chip')).toHaveText('Resolved')
        await expect(customer.page.getByTestId('message-thread')).toContainText(fix)
        await expect(customer.page.getByTestId('status-chip').first()).toHaveText('Resolved')
        expect(await isAlive(customer.page)).toBe(true)
        // Positive control: the updates came over the socket, not from a refetch after a reload.
        const events = frames.map((f) => JSON.parse(f) as { event?: string; data?: unknown })
        const carried = (event: string, text: string) => events.some((e) => e.event === event && JSON.stringify(e.data).includes(text))
        expect(carried('message.created', `PUBLIC-${id}`)).toBe(true)
        expect(carried('ticket.status_changed', '"to":"resolved"')).toBe(true)
      })

      // 7. The agent's reply reached the customer's mailbox (SMTP → Mailpit).
      await phase('reply mail in Mailpit', async () => {
        await findInMail(request, email, new RegExp(`PUBLIC-${id}`), 30_000)
      })
      await customer.context.close()
    } finally {
      await agent.context.close()
      await ak.deleteUser(idpUser)
    }
  })
})
