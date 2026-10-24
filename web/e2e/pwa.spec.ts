// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Installable app (PWA): each area links its own manifest, Chrome itself finds
 * nothing that blocks installing it, the offline-only service worker registers,
 * and the admin's "Generate app icons" button draws real PNGs that the
 * manifest then serves (and "Reset" goes back to the defaults).
 */
import type { Page } from '@playwright/test'
import { test, expect, ADMIN, loginAs } from './fixtures'

/** Chrome's own verdict (CDP): manifest parse errors and installability blockers. */
async function installability(page: Page) {
  const cdp = await page.context().newCDPSession(page)
  const manifest = (await cdp.send('Page.getAppManifest')) as { url: string; errors: { message: string }[] }
  const { installabilityErrors } = (await cdp.send('Page.getInstallabilityErrors')) as { installabilityErrors: { errorId: string }[] }
  await cdp.detach()
  // Playwright contexts are off the record; that is the browser profile, not the app.
  return { url: manifest.url, errors: manifest.errors.map((e) => e.message), blockers: installabilityErrors.map((e) => e.errorId).filter((id) => id !== 'in-incognito') }
}

async function manifest(page: Page, area: string) {
  const res = await page.request.get(`/manifest.webmanifest?area=${area}`)
  expect(res.headers()['content-type']).toBe('application/manifest+json')
  return (await res.json()) as { start_url: string; scope: string; icons: { src: string; sizes: string; purpose: string }[] }
}

test.describe('installable app', () => {
  test.skip(({ browserName }) => browserName !== 'chromium', 'CDP installability check')

  test('portal and agent are installable, each with its own manifest', async ({ page }) => {
    await page.goto('/login')
    await page.waitForLoadState('networkidle')
    let r = await installability(page)
    expect(r.url).toMatch(/\/manifest\.webmanifest\?area=portal$/)
    expect(r.errors).toEqual([])
    expect(r.blockers).toEqual([])
    await expect(page.locator('link[rel="manifest"]')).toHaveCount(1)
    await expect(page.locator('link[rel="apple-touch-icon"]')).toHaveAttribute('href', /\.png$/)
    await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute('content', /^#[0-9a-fA-F]{6}$/)

    await page.goto('/agent/login')
    await page.waitForLoadState('networkidle')
    r = await installability(page)
    expect(r.url).toMatch(/\/manifest\.webmanifest\?area=agent$/)
    expect(r.errors).toEqual([])
    expect(r.blockers).toEqual([])
    const m = await manifest(page, 'agent')
    expect([m.start_url, m.scope]).toEqual(['/agent/', '/agent/'])

    // The offline-only worker registers in the production build and is served fresh.
    await expect.poll(() => page.evaluate(async () => !!(await navigator.serviceWorker.getRegistration('/')))).toBe(true)
    const sw = await page.request.get('/sw.js')
    expect(sw.headers()['cache-control']).toBe('no-cache')
    expect(sw.headers()['content-type']).toMatch(/javascript/)
    expect((await page.request.get('/offline.html')).ok()).toBe(true)
  })

  test('admin generates app icons from the logo, then resets them', async ({ page, isMobile }) => {
    test.skip(isMobile, 'instance-wide branding change: once is enough')
    await loginAs(page, 'staff', ADMIN)
    await page.goto('/agent/admin/branding')
    const card = page.getByTestId('pwa-icons-card')
    await card.scrollIntoViewIfNeeded()
    await card.getByTestId('pwa-icons-generate').click()
    await expect(card.getByTestId('pwa-icons-reset')).toBeVisible()

    let m = await manifest(page, 'portal')
    expect(m.icons.map((i) => i.src).every((s) => /^\/branding\/[0-9a-f]{64}\.png\?v=[0-9a-f]{8}$/.test(s))).toBe(true)
    for (const icon of m.icons) {
      const res = await page.request.get(icon.src)
      expect(res.headers()['content-type']).toBe('image/png')
    }
    await page.goto('/login')
    await page.waitForLoadState('networkidle')
    expect((await installability(page)).blockers).toEqual([])

    await page.goto('/agent/admin/branding')
    await page.getByTestId('pwa-icons-reset').click()
    await page.getByTestId('confirm-ok').click()
    await expect(page.getByTestId('pwa-icons-reset')).toHaveCount(0)
    m = await manifest(page, 'portal')
    expect(m.icons.map((i) => i.src)).toEqual(['/pwa/icon-192.png', '/pwa/icon-512.png', '/pwa/maskable-512.png'])
  })
})
