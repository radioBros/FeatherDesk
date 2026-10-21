// SPDX-License-Identifier: AGPL-3.0-only

/**
 * FE-049 first-run wizard. Needs a FRESH instance with no admin and no
 * ST_BOOTSTRAP_ADMIN_* (the default E2E server bootstraps an admin, which
 * closes /setup). Provide it with:
 *   E2E_SETUP_BASE_URL  base URL of the fresh instance
 *   E2E_SETUP_TOKEN     the "setup token: …" line from that instance's log
 */
import { test, expect, watchConsole, a11y } from '../fixtures'
import type { Page } from '@playwright/test'

const BASE = process.env.E2E_SETUP_BASE_URL ?? ''
const TOKEN = process.env.E2E_SETUP_TOKEN ?? ''

/** FE-032: axe on the step that is showing (the wizard is a page of its own, outside both areas). */
async function scan(page: Page, step: string, found: Record<string, unknown>) {
  await page.waitForTimeout(300)
  const v = await a11y(page)
  if (v.length) found[step] = v.map((x) => ({ id: x.id, nodes: x.nodes.slice(0, 3).map((n) => n.target.join(' ')) }))
}

test.describe('first-run setup wizard', () => {
  test('the default instance (bootstrapped admin) keeps /setup closed', async ({ page }) => {
    test.skip(!!BASE, 'covered by the fresh-instance test below')
    await page.goto('/setup')
    await expect(page).not.toHaveURL(/\/setup/)
  })

  test('token → admin → branding → SMTP skip → SSO → done', async ({ page }) => {
    test.fixme(!BASE || !TOKEN, 'needs E2E_SETUP_BASE_URL (fresh instance without ST_BOOTSTRAP_ADMIN_*) and E2E_SETUP_TOKEN from its log')
    test.skip(test.info().project.name !== 'chromium', 'one-shot: setup can complete only once per instance')
    const problems = watchConsole(page)
    const axe: Record<string, unknown> = {}
    await page.goto(new URL('/setup', BASE).toString())
    await expect(page.getByTestId('setup-stepper')).toBeVisible()
    await scan(page, 'token', axe)

    // Every step validates before advancing.
    await page.getByTestId('setup-token-form').getByTestId('setup-next').click()
    await expect(page.getByText('This field is required').first()).toBeVisible()
    await page.getByTestId('setup-token').locator('input').fill(TOKEN)
    await page.getByTestId('setup-token-form').getByTestId('setup-next').click()

    await expect(page.getByTestId('setup-admin-name')).toBeVisible()
    await scan(page, 'admin', axe)
    await page.getByTestId('setup-admin-name').locator('input').fill('E2E Admin')
    await page.getByTestId('setup-admin-email').locator('input').fill('setup-admin@e2e.test')
    await page.getByTestId('setup-admin-password').locator('input').fill('setup-admin-password-1')
    await page.getByTestId('setup-admin-confirm').locator('input').fill('setup-admin-password-1')
    await page.getByTestId('setup-admin-form').getByTestId('setup-next').click()

    await expect(page.getByTestId('setup-product-name')).toBeVisible()
    await scan(page, 'branding', axe)
    await page.getByTestId('setup-product-name').locator('input').fill('E2E Desk')
    await page.getByTestId('setup-branding-form').getByTestId('setup-next').click()

    // SMTP: Test validates first (no request with an empty host), then skip.
    let tested = false
    page.on('request', (r) => {
      if (r.url().includes('/admin/email/test')) tested = true
    })
    await expect(page.getByTestId('smtp-host')).toBeVisible()
    await scan(page, 'smtp', axe)
    await page.getByTestId('smtp-host').locator('input').fill('')
    await page.getByTestId('smtp-test').click()
    await expect(page.getByTestId('setup-smtp-form').getByText('This field is required').first()).toBeVisible()
    expect(tested).toBe(false)
    await page.getByTestId('smtp-skip').click()

    await expect(page.getByTestId('setup-sso')).toContainText('/api/v1/auth/oidc/staff/callback')
    await scan(page, 'sso', axe)
    await page.getByTestId('setup-sso').getByTestId('setup-next').click()
    // The wizard itself must be clean; what the agent panel loads afterwards is that area's concern.
    const wizardProblems = [...problems]
    await expect(page.getByTestId('setup-finish')).toBeVisible()
    await scan(page, 'done', axe)
    expect(axe).toEqual({})
    await page.getByTestId('setup-finish').click()
    await expect(page).toHaveURL(/\/agent/)

    // The password never reached sessionStorage, and /setup is closed now.
    const stored = await page.evaluate(() => JSON.stringify(sessionStorage))
    expect(stored).not.toContain('setup-admin-password-1')
    await page.goto(new URL('/setup', BASE).toString())
    await expect(page).not.toHaveURL(/\/setup/)
    expect(wizardProblems).toEqual([])
  })
})
