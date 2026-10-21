// SPDX-License-Identifier: AGPL-3.0-only

/**
 * FE-040 E2E: an admin creates a webhook through the UI, sees the signing
 * secret exactly once, and an http:// target is rejected client-side.
 * Needs the full backend (runs during integration).
 */
import { test, expect, loginAs, ADMIN } from '../fixtures'

test.describe('admin webhooks', () => {
  test.skip(({ isMobile }) => isMobile, 'admin pages are covered on desktop')

  test('create a webhook: secret shown once, then gone', async ({ page }) => {
    await loginAs(page, 'staff', ADMIN)
    await page.goto('/agent/admin/webhooks')
    await expect(page.getByTestId('webhook-new')).toBeVisible()

    // The server resolves the host (422 `unresolvable` otherwise): use a resolvable one.
    const url = `${process.env.E2E_WEBHOOK_URL ?? 'https://example.com/hooks'}/e2e-${Date.now()}`
    await page.getByTestId('webhook-new').click()
    await page.getByTestId('webhook-url').locator('input').fill(url)
    const events = page.getByTestId('webhook-events')
    await events.locator('input').click()
    await page.getByRole('option', { name: 'ticket.created', exact: true }).click()
    await page.getByRole('option', { name: 'message.created', exact: true }).click()
    await page.keyboard.press('Escape')

    const created = page.waitForResponse((r) => r.url().endsWith('/api/v1/admin/webhooks') && r.request().method() === 'POST')
    await page.getByTestId('webhook-save').click()
    const res = await created
    expect(res.status()).toBe(201)
    const { secret } = (await res.json()) as { secret: string }
    expect(secret).toBeTruthy()

    await expect(page.getByTestId('webhook-secret-dialog')).toBeVisible()
    await expect(page.getByTestId('secret-value').locator('input')).toHaveValue(secret)
    await page.getByTestId('webhook-secret-close').click()
    await expect(page.getByTestId('webhook-secret-dialog')).toBeHidden()

    await expect(page.getByTestId('webhook-row').filter({ hasText: url })).toBeVisible()
    expect(await page.content()).not.toContain(secret)
  })

  test('an http:// URL is rejected before any request', async ({ page }) => {
    await loginAs(page, 'staff', ADMIN)
    await page.goto('/agent/admin/webhooks')
    let posted = false
    page.on('request', (r) => {
      if (r.method() === 'POST' && r.url().endsWith('/api/v1/admin/webhooks')) posted = true
    })
    await page.getByTestId('webhook-new').click()
    await page.getByTestId('webhook-url').locator('input').fill('http://hooks.example.com/insecure')
    await page.getByTestId('webhook-save').click()
    await expect(page.getByText('Enter an https:// URL').first()).toBeVisible()
    expect(posted).toBe(false)
  })
})
