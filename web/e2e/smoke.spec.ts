// SPDX-License-Identifier: AGPL-3.0-only

import { test, expect, watchConsole, a11y } from './fixtures'

test.describe('smoke', () => {
  test('portal login renders without console or CSP errors', async ({ page }) => {
    const problems = watchConsole(page)
    await page.goto('/')
    await expect(page).toHaveURL(/\/(login|setup)/)
    if (page.url().includes('/login')) {
      await expect(page.getByTestId('login-form')).toBeVisible()
      await expect(page.getByTestId('portal-footer')).toBeVisible()
    }
    expect(problems).toEqual([])
  })

  test('agent login renders without console or CSP errors', async ({ page }) => {
    const problems = watchConsole(page)
    await page.goto('/agent/login')
    await expect(page.locator('[data-testid="login-form"], [data-testid="sso-button"]').first()).toBeVisible()
    expect(problems).toEqual([])
  })

  test('empty login submit shows field errors and sends no login request', async ({ page }) => {
    await page.goto('/login')
    let posted = false
    page.on('request', (r) => {
      if (r.url().includes('/api/v1/auth/login')) posted = true
    })
    await page.getByTestId('login-submit').click()
    await expect(page.getByText('This field is required').first()).toBeVisible()
    expect(posted).toBe(false)
  })

  test('login pages have no serious accessibility violations', async ({ page }) => {
    for (const path of ['/login', '/agent/login']) {
      await page.goto(path)
      await page.waitForLoadState('networkidle')
      expect(await a11y(page), path).toEqual([])
    }
  })
})
