// SPDX-License-Identifier: AGPL-3.0-only

/**
 * CON-018: the generated API reference (/api/docs) is accessible, offers the
 * spec download, and fits a phone without horizontal page scroll.
 */
import { test, expect, a11y } from './fixtures'

test.describe('API reference', () => {
  test.skip(({ isMobile }) => isMobile, 'the phone width is checked below on desktop chromium')

  test('accessible, downloadable spec, fits 412 px', async ({ page }) => {
    await page.goto('/api/docs')
    await expect(page.getByRole('heading', { level: 1 })).toContainText('API reference')
    const download = page.getByRole('link', { name: /Download openapi\.yaml/ })
    await expect(download).toHaveAttribute('href', '/api/v1/openapi.yaml')
    await expect(download).toHaveAttribute('download', /\.yaml$/)
    // An operation card: method, path, curl and both examples.
    const op = page.locator('#createAgentTicket')
    await expect(op).toContainText('/api/v1/agent/tickets')
    await expect(op.locator('pre').first()).toContainText("curl -X POST 'https://help.example.com/api/v1/agent/tickets'")
    await expect(op).toContainText('"subject": "VPN drops every 10 minutes"')
    await a11y(page)

    await page.setViewportSize({ width: 412, height: 860 })
    await page.goto('/api/docs#createAgentTicket')
    expect(await page.evaluate(() => document.scrollingElement!.scrollWidth)).toBeLessThanOrEqual(412)
  })
})
