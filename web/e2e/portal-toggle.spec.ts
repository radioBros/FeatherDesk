// SPDX-License-Identifier: AGPL-3.0-only

/**
 * support.portal_enabled: an admin switches the customer portal off in
 * Admin > Settings and only admin and staff can sign in. The portal routes lead
 * to /agent/login, `/` goes to /agent, the public help centre keeps following
 * its own `kb_enabled`, customer API entry points answer 404 `portal-disabled`,
 * and a signed-in customer is thrown out (needs a customer account: Mailpit or
 * E2E_CUSTOMER_EMAIL/E2E_CUSTOMER_PASSWORD). Switching it back on restores the
 * portal. The instance is shared, so the switch is always put back on.
 */
import { test, expect, loginAs, ADMIN, clientIp, a11y } from './fixtures'
import { apiFor, customerAccount } from './agent/helpers'
import type { Browser, Page } from '@playwright/test'

test.describe.configure({ mode: 'serial' })
test.skip(({ isMobile }) => isMobile, 'the settings page is covered on desktop; the portal redirects are viewport-independent')

async function adminApi(browser: Browser) {
  const context = await browser.newContext({ extraHTTPHeaders: clientIp() })
  const page = await context.newPage()
  await loginAs(page, 'staff', ADMIN)
  return { context, page, api: await apiFor(page) }
}

const config = async (page: Page) => (await page.request.get('/api/v1/auth/config')).json() as Promise<{ portal_enabled: boolean; customer: { local: boolean } }>

test.afterAll(async ({ browser }) => {
  const { context, api } = await adminApi(browser)
  await api.patch('/admin/settings', { portal_enabled: true })
  await context.close()
})

test('the toggle in Admin > Settings closes the portal and opens it again', async ({ page, browser }) => {
  await loginAs(page, 'staff', ADMIN)
  await page.goto('/agent/admin/settings')
  const sw = page.getByTestId('portal-enabled').locator('input')
  await expect(sw).toBeChecked()
  await expect(page.getByTestId('portal-enabled')).toContainText('only admins and staff sign in')
  await sw.setChecked(false, { force: true })
  await expect(page.getByTestId('portal-off-warning')).toBeVisible()
  const saved = page.waitForResponse((r) => r.url().endsWith('/api/v1/admin/settings') && r.request().method() === 'PATCH')
  await page.getByTestId('settings-save').click()
  expect((await saved).status()).toBe(200)
  await expect.poll(async () => (await config(page)).portal_enabled).toBe(false)

  // A visitor: every portal URL leads to the staff sign-in, "/" included.
  const visitor = await browser.newContext({ extraHTTPHeaders: clientIp() })
  const v = await visitor.newPage()
  for (const path of ['/', '/login', '/register', '/forgot-password', '/tickets', '/tickets/new', '/profile']) {
    await v.goto(path)
    await expect(v, path).toHaveURL(/\/agent\/login(\?|$)/)
  }
  await expect(v.getByTestId('agent-login-main')).toBeVisible()
  expect(await config(v)).toMatchObject({ portal_enabled: false, customer: { local: false } })

  // The help centre follows its own setting and shows no customer entry point.
  await v.goto('/kb')
  await expect(v).toHaveURL(/\/kb$/)
  await expect(v.getByTestId('kb-search')).toBeVisible()
  await expect(v.getByTestId('staff-sign-in')).toHaveAttribute('href', '/agent/login')
  await expect(v.locator('a[href="/login"], a[href="/tickets/new"], a[href="/register"]')).toHaveCount(0)
  expect(await a11y(v)).toEqual([])

  // The API refuses every customer entry point with `portal-disabled`.
  for (const [method, path, data] of [
    ['post', '/api/v1/auth/login', { audience: 'customer', email: 'c@e2e.test', password: 'whatever-123456' }],
    ['post', '/api/v1/auth/register', { email: 'c@e2e.test', name: 'C', password: 'whatever-123456-xyz' }],
    ['post', '/api/v1/auth/magic-link', { email: 'c@e2e.test' }],
    ['get', '/api/v1/auth/oidc/customer/start', undefined],
  ] as const) {
    const res = method === 'post' ? await v.request.post(path, { data, headers: { Origin: new URL(v.url()).origin } }) : await v.request.get(path)
    expect(res.status(), path).toBe(404)
    expect((await res.json()).type, path).toMatch(/\/problems\/portal-disabled$/)
  }
  // Staff are unaffected.
  expect((await v.request.post('/api/v1/auth/login', { data: { audience: 'staff', ...ADMIN } })).status()).toBe(200)
  await v.goto('/agent/tickets')
  await expect(v).toHaveURL(/\/agent\/tickets/)
  await visitor.close()

  // Back on.
  await page.goto('/agent/admin/settings')
  await page.getByTestId('portal-enabled').locator('input').setChecked(true, { force: true })
  const again = page.waitForResponse((r) => r.url().endsWith('/api/v1/admin/settings') && r.request().method() === 'PATCH')
  await page.getByTestId('settings-save').click()
  expect((await again).status()).toBe(200)
  const back = await browser.newContext({ extraHTTPHeaders: clientIp() })
  const b = await back.newPage()
  await b.goto('/')
  await expect(b).toHaveURL(/\/login(\?|$)/)
  await expect(b.getByTestId('login-form')).toBeVisible()
  expect((await config(b)).portal_enabled).toBe(true)
  await back.close()
})

test('a signed-in customer loses the session when the portal is switched off', async ({ browser }) => {
  const seed = await browser.newContext({ extraHTTPHeaders: clientIp() })
  const creds = await customerAccount(await seed.newPage())
  await seed.close()
  test.skip(!creds, 'needs E2E_MAILPIT_URL or E2E_CUSTOMER_EMAIL/E2E_CUSTOMER_PASSWORD for a customer')

  const customer = await browser.newContext({ extraHTTPHeaders: clientIp() })
  const c = await customer.newPage()
  await loginAs(c, 'customer', creds!)
  await c.goto('/tickets')
  await expect(c).toHaveURL(/\/tickets$/)
  expect((await c.request.get('/api/v1/portal/tickets')).status()).toBe(200)

  const { context, api } = await adminApi(browser)
  await api.patch('/admin/settings', { portal_enabled: false })
  await expect.poll(async () => (await c.request.get('/api/v1/me')).status()).toBe(401)
  expect((await c.request.get('/api/v1/portal/tickets')).status()).toBe(401)
  await c.goto('/tickets')
  await expect(c).toHaveURL(/\/agent\/login/)
  await api.patch('/admin/settings', { portal_enabled: true })
  await context.close()
  await customer.close()
})
