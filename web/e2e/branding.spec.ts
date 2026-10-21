// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Runtime branding (FE-002, FE-047, D-13) against the real binary.
 *
 * Every test that changes the instance branding lives in this one file and
 * runs serially on desktop only, so no other worker sees a half-applied
 * change; each restores the original branding in `finally`.
 */
import { test, expect, loginAs, clientIp, ADMIN } from './fixtures'
import type { Page } from '@playwright/test'
import { customerAccount, signIn } from './agent/helpers'

test.describe.configure({ mode: 'serial' })

// FE-002 / D-13: fonts and assets are self-hosted — no third-party origins at all.
test('no request leaves the instance origin', async ({ page, baseURL }) => {
  const origin = new URL(baseURL!).origin
  const foreign: string[] = []
  page.on('request', (r) => {
    const u = new URL(r.url())
    if (u.protocol.startsWith('http') && u.origin !== origin) foreign.push(r.url())
  })
  for (const path of ['/login', '/agent/login', '/kb']) {
    await page.goto(path)
    await page.waitForLoadState('networkidle')
  }
  expect(foreign).toEqual([])
  expect(foreign.some((u) => u.includes('fonts.googleapis.com'))).toBe(false)
})

const rgb = (hex: string) =>
  [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16)).join(',')

/** The CSS custom property Vuetify sets for the primary colour, whitespace removed. */
const primaryOf = (page: Page) =>
  page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--v-theme-primary').replace(/\s/g, ''))

interface FirstFrame {
  primary: string
  title: string
  styled: boolean
  dark: boolean
  colorScheme: string
}

/**
 * Records the document state at the moment the app inserts its first element
 * into #app. A MutationObserver callback runs as a microtask right after that
 * insertion, before the browser can render a frame: whatever it sees is what
 * the first painted frame shows.
 */
async function recordFirstFrame(page: Page) {
  await page.addInitScript(() => {
    const w = window as unknown as { __firstFrame?: FirstFrame | null }
    w.__firstFrame = null
    const obs = new MutationObserver(() => {
      const app = document.getElementById('app')
      const el = app?.querySelector('.v-application') ?? app?.firstElementChild
      if (!el || w.__firstFrame) return
      const root = getComputedStyle(document.documentElement)
      w.__firstFrame = {
        primary: root.getPropertyValue('--v-theme-primary').replace(/\s/g, ''),
        title: document.title,
        // Vuetify's stylesheet is applied (.v-application is a flex column) — no unstyled frame.
        styled: getComputedStyle(el).display === 'flex',
        dark: el.classList.contains('v-theme--dark'),
        colorScheme: root.colorScheme,
      }
      obs.disconnect()
    })
    obs.observe(document, { childList: true, subtree: true })
  })
}

test.describe('runtime branding', () => {
  test.skip(({ isMobile }) => isMobile, 'instance-wide branding changes run once, on desktop')

  test('brand colours, title and theme are in place in the first painted frame (no flash of the default theme)', async ({ page, browser }) => {
    const admin = await signIn(browser, 'staff', ADMIN)
    const me = await (await admin.page.request.get('/api/v1/me')).json()
    const headers = { 'X-CSRF-Token': me.csrf_token, Origin: new URL(test.info().project.use.baseURL!).origin }
    const original = await (await admin.page.request.get('/api/v1/admin/branding')).json()
    const { assets: _assets, ...restore } = original
    const name = `First Paint ${Date.now()}`
    const colors = { base: '#2e7d32', portal: '#6a1b9a', agent: '#00695c' }
    try {
      const put = await admin.page.request.put('/api/v1/admin/branding', {
        headers,
        data: { ...restore, product_name: name, colors: { ...restore.colors, primary: colors.base }, portal_colors: { primary: colors.portal }, agent_colors: { primary: colors.agent } },
      })
      expect(put.ok(), await put.text()).toBeTruthy()

      await recordFirstFrame(page)
      for (const [path, hex] of [['/login', colors.portal], ['/agent/login', colors.agent], ['/kb', colors.portal]] as const) {
        await page.goto(path)
        await expect.poll(() => page.evaluate(() => (window as unknown as { __firstFrame?: FirstFrame }).__firstFrame)).not.toBeNull()
        const first = (await page.evaluate(() => (window as unknown as { __firstFrame: FirstFrame }).__firstFrame))!
        expect(first.primary, `${path}: primary in the first frame`).toBe(rgb(hex))
        expect(first.primary, `${path}: never Vuetify's default primary`).not.toBe(rgb('#1a73e8'))
        expect(first.styled, `${path}: Vuetify styles applied in the first frame`).toBe(true)
        expect(first.title, `${path}: title in the first frame`).toContain(name)
        // And it stays that colour once the page has settled.
        expect(await primaryOf(page)).toBe(rgb(hex))
      }

      // A visitor who chose dark mode gets the dark theme and a dark canvas in the first frame.
      const dark = await browser.newContext({ extraHTTPHeaders: clientIp(), colorScheme: 'light' })
      const darkPage = await dark.newPage()
      await darkPage.addInitScript(() => {
        try {
          localStorage.setItem('st.theme', 'dark')
        } catch {
          /* ignore */
        }
      })
      await recordFirstFrame(darkPage)
      await darkPage.goto('/login')
      await expect.poll(() => darkPage.evaluate(() => (window as unknown as { __firstFrame?: FirstFrame }).__firstFrame)).not.toBeNull()
      const firstDark = (await darkPage.evaluate(() => (window as unknown as { __firstFrame: FirstFrame }).__firstFrame))!
      expect(firstDark.dark).toBe(true)
      expect(firstDark.colorScheme).toBe('dark')
      expect(firstDark.styled).toBe(true)
      await dark.close()
    } finally {
      const res = await admin.page.request.put('/api/v1/admin/branding', { data: restore, headers })
      expect(res.ok(), await res.text()).toBeTruthy()
      await admin.context.close()
    }
  })

  test('a branding change reaches an open portal page over the WebSocket, without reload', async ({ browser, page }) => {
    const creds = await customerAccount(page)
    test.skip(!creds, 'needs E2E_CUSTOMER_EMAIL/E2E_CUSTOMER_PASSWORD or E2E_MAILPIT_URL')
    const admin = await signIn(browser, 'staff', ADMIN)
    const me = await (await admin.page.request.get('/api/v1/me')).json()
    const headers = { 'X-CSRF-Token': me.csrf_token, Origin: new URL(test.info().project.use.baseURL!).origin }
    const original = await (await admin.page.request.get('/api/v1/admin/branding')).json()
    const { assets: _assets, ...restore } = original
    const name = `Live Portal ${Date.now()}`
    const customer = await signIn(browser, 'customer', creds!)
    try {
      const frames: string[] = []
      customer.page.on('websocket', (ws) => ws.on('framereceived', (f) => frames.push(String(f.payload))))
      await customer.page.goto('/tickets')
      await expect(customer.page.getByTestId('portal-footer')).toBeVisible()
      const before = await primaryOf(customer.page)
      await customer.page.evaluate(() => ((window as unknown as { __live: string }).__live = 'alive'))

      const put = await admin.page.request.put('/api/v1/admin/branding', {
        headers,
        data: { ...restore, product_name: name, portal_colors: { primary: '#ad1457' } },
      })
      expect(put.ok(), await put.text()).toBeTruthy()

      await expect(customer.page).toHaveTitle(new RegExp(name), { timeout: 10_000 })
      await expect.poll(() => primaryOf(customer.page)).toBe(rgb('#ad1457'))
      expect(before).not.toBe(rgb('#ad1457'))
      expect(frames.some((f) => f.includes('settings.branding_changed'))).toBe(true)
      expect(await customer.page.evaluate(() => (window as unknown as { __live?: string }).__live)).toBe('alive')
    } finally {
      const res = await admin.page.request.put('/api/v1/admin/branding', { data: restore, headers })
      expect(res.ok(), await res.text()).toBeTruthy()
      await Promise.all([customer.context.close(), admin.context.close()])
    }
  })

  // FE-047 (integration): an admin's change applies live in the tab that saved it and, over
  // the `me` topic, in another agent tab that is already open.
  test('an admin branding change applies live in both agent tabs', async ({ page, context }) => {
    const me = await loginAs(page, 'staff', ADMIN)
    const headers = { 'X-CSRF-Token': me.csrf_token, Origin: new URL(test.info().project.use.baseURL!).origin }
    const original = await (await page.request.get('/api/v1/admin/branding')).json()
    const name = `E2E Brand ${Date.now()}`

    try {
      const other = await context.newPage()
      await other.goto('/agent/tickets')
      await expect(other.getByTestId('agent-drawer')).toBeVisible()
      await other.evaluate(() => ((window as unknown as { __live: string }).__live = 'alive'))

      await page.goto('/agent/admin/branding')
      await expect(page.getByTestId('branding-form')).toBeVisible()
      await page.getByTestId('brand-product-name').locator('input').fill(name)
      await page.getByTestId('brand-primary').locator('input').fill('#c62828')
      // The preview pane follows the unsaved draft.
      await expect(page.getByTestId('preview-portal-bar')).toContainText(name)
      await page.getByTestId('branding-save').click()

      await expect(page).toHaveTitle(new RegExp(name))
      await expect.poll(() => primaryOf(page)).toBe('198,40,40')

      await expect(other).toHaveTitle(new RegExp(name), { timeout: 10_000 })
      await expect.poll(() => primaryOf(other)).toBe('198,40,40')
      expect(await other.evaluate(() => (window as unknown as { __live?: string }).__live)).toBe('alive')
    } finally {
      const { assets: _assets, ...restore } = original
      const res = await page.request.put('/api/v1/admin/branding', { data: restore, headers })
      expect(res.ok(), await res.text()).toBeTruthy()
    }
  })

  test('an SVG email logo is refused before upload', async ({ page }) => {
    await loginAs(page, 'staff', ADMIN)
    await page.goto('/agent/admin/branding')
    let uploaded = false
    page.on('request', (r) => {
      if (r.method() === 'POST' && r.url().includes('/admin/branding/assets/')) uploaded = true
    })
    await page
      .getByTestId('slot-email_logo-input')
      .locator('input[type="file"]')
      .setInputFiles({ name: 'logo.svg', mimeType: 'image/svg+xml', buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg"/>') })
    await expect(page.getByTestId('slot-email_logo')).toContainText('This file type is not allowed')
    expect(uploaded).toBe(false)
  })
})
