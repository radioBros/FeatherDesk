// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Portal behaviour that works against the default E2E server (no SMTP):
 * email-disabled states, generic login errors, public KB access, a11y.
 */
import { test, expect, watchConsole, a11y } from '../fixtures'

test.describe('portal basics', () => {
  test('with email disabled, Register/Forgot are hidden and their routes redirect to /login', async ({ page }) => {
    const cfg = await (await page.request.get('/api/v1/auth/config')).json()
    test.skip(cfg.email_enabled === true, 'this instance has SMTP configured')
    await page.goto('/login')
    await expect(page.getByTestId('login-form')).toBeVisible()
    await expect(page.getByTestId('register-link')).toHaveCount(0)
    await expect(page.getByTestId('forgot-link')).toHaveCount(0)
    for (const path of ['/register', '/forgot-password']) {
      await page.goto(path)
      await expect(page).toHaveURL(/\/login$/)
    }
  })

  test('a wrong password shows the generic error', async ({ page }) => {
    await page.goto('/login')
    await page.getByTestId('login-email').locator('input').fill(`nobody-${Date.now()}@e2e.test`)
    await page.getByTestId('login-password').locator('input').fill('not-the-password')
    await page.getByTestId('login-submit').click()
    await expect(page.getByTestId('form-error')).toHaveText('Email or password is not correct.')
  })

  test('the help center opens signed out, without console errors or serious a11y issues', async ({ page }) => {
    const problems = watchConsole(page)
    await page.goto('/kb')
    await expect(page).toHaveURL(/\/kb$/)
    await expect(page.getByTestId('kb-search')).toBeVisible()
    await page.waitForLoadState('networkidle')
    expect(await a11y(page)).toEqual([])
    expect(problems).toEqual([])
  })

  test('deep links into the portal keep the redirect through sign-in', async ({ page }) => {
    await page.goto('/tickets/new')
    await expect(page).toHaveURL(/\/login\?redirect=(%2F|\/)tickets(%2F|\/)new/)
  })
})
