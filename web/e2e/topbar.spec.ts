// SPDX-License-Identifier: AGPL-3.0-only

/**
 * The top bar of both areas keeps its actions apart (>= 8 px), at least 40 px
 * big, inside the viewport with room at the edges, on a desktop and on a phone.
 * On a phone the theme and language choices move into the user menu.
 */
import { test, expect, loginAs, ADMIN, clientIp } from './fixtures'
import type { Page } from '@playwright/test'

interface Box { name: string; left: number; right: number; width: number; height: number }

async function barButtons(page: Page): Promise<{ boxes: Box[]; width: number }> {
  await page.locator('header.v-toolbar').first().waitFor()
  return page.evaluate(() => {
    const bar = document.querySelector('header.v-toolbar')!
    const boxes = [...bar.querySelectorAll<HTMLElement>('button, a.v-btn')]
      .map((el) => ({ el, r: el.getBoundingClientRect() }))
      .filter(({ r }) => r.width > 0)
      .map(({ el, r }) => ({ name: el.getAttribute('aria-label') ?? el.textContent?.trim() ?? '', left: r.left, right: r.right, width: r.width, height: r.height }))
    return { boxes, width: document.documentElement.clientWidth }
  })
}

function expectComfortable({ boxes, width }: { boxes: Box[]; width: number }) {
  expect(boxes.length).toBeGreaterThan(1)
  for (const b of boxes) {
    expect(b.width, `${b.name} width`).toBeGreaterThanOrEqual(39.5)
    expect(b.height, `${b.name} height`).toBeGreaterThanOrEqual(39.5)
    expect(b.left, `${b.name} inside left`).toBeGreaterThanOrEqual(8)
    expect(b.right, `${b.name} inside right`).toBeLessThanOrEqual(width - 8)
  }
  const sorted = [...boxes].sort((a, b) => a.left - b.left)
  const actions = sorted.filter((b) => b.left > width / 2 || width < 600)
  for (let i = 1; i < actions.length; i++) expect(actions[i]!.left - actions[i - 1]!.right, `${actions[i - 1]!.name} to ${actions[i]!.name}`).toBeGreaterThanOrEqual(7.5)
  expect(width - sorted.at(-1)!.right, 'right edge padding').toBeGreaterThanOrEqual(12)
}

for (const [label, size] of [['desktop', { width: 1280, height: 720 }], ['phone', { width: 360, height: 740 }]] as const) {
  test.describe(`top bar, ${label}`, () => {
    test.use({ viewport: size })
    test.skip(({ isMobile }) => isMobile, 'the viewport is set per test')

    test('agent panel', async ({ page }) => {
      await loginAs(page, 'staff', ADMIN)
      await page.goto('/agent/tickets')
      expectComfortable(await barButtons(page))
    })

    test('portal (signed out, help centre)', async ({ browser }) => {
      const ctx = await browser.newContext({ viewport: size, extraHTTPHeaders: clientIp() })
      const page = await ctx.newPage()
      await page.goto('/kb')
      expectComfortable(await barButtons(page))
      await ctx.close()
    })
  })
}

test('on a phone the theme and language choices live in the user menu', async ({ page }) => {
  test.skip(test.info().project.name === 'mobile', 'runs once, with its own viewport')
  await page.setViewportSize({ width: 360, height: 740 })
  await loginAs(page, 'staff', ADMIN)
  await page.goto('/agent/tickets')
  await expect(page.getByRole('button', { name: 'Language' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Theme' })).toHaveCount(0)
  await page.getByTestId('user-menu').click()
  await page.getByTestId('menu-theme').getByText('Theme').click()
  await page.getByText('Dark', { exact: true }).click()
  await expect(page.locator('.v-theme--dark').first()).toBeVisible()
})

// The unread badge sits on the bell's corner and must be fully visible: inside the viewport, not clipped by the
// button or the bar, and not overlapping the neighbouring buttons. Before/after shots: test-results/badge/<BADGE_SHOT>/.
const SHOT = process.env.BADGE_SHOT ?? 'after'
const CUSTOMER = {
  email: process.env.E2E_CUSTOMER_EMAIL ?? 'chloe.chen@acme.test',
  password: process.env.E2E_CUSTOMER_PASSWORD ?? 'demo-password-123',
}

async function expectBadgeVisible(page: Page, shot: string) {
  const count = page.getByTestId('notification-count')
  await expect(count).toBeVisible()
  const badgeEl = page.locator('[data-testid="notification-badge"] .v-badge__badge')
  // The badge scales in (a transition): measure once its box stopped moving.
  await badgeEl.evaluate(
    (el) =>
      new Promise<void>((done) => {
        let last = ''
        let still = 0
        const tick = () => {
          const r = el.getBoundingClientRect()
          const now = `${r.left}|${r.width}|${r.top}`
          still = now === last ? still + 1 : 0
          last = now
          if (still >= 5) done()
          else requestAnimationFrame(tick)
        }
        tick()
      }),
  )
  const r = await badgeEl.evaluate((el) => {
    const b = el.getBoundingClientRect()
    const bell = document.querySelector('[data-testid="notification-bell"]')!.getBoundingClientRect()
    const bar = document.querySelector('header.v-toolbar')!.getBoundingClientRect()
    const vw = document.documentElement.clientWidth
    // The badge is a pill: probe its four extreme edge points (inset 1.5 px); each must hit the badge itself,
    // so nothing clips or covers it.
    const mx = (b.left + b.right) / 2
    const my = (b.top + b.bottom) / 2
    const corners = [[b.left + 1.5, my], [b.right - 1.5, my], [mx, b.top + 1.5], [mx, b.bottom - 1.5]]
    const hits = corners.map(([x, y]) => {
      const top = document.elementFromPoint(x!, y!)
      return !!top && (el === top || el.contains(top))
    })
    const others = [...document.querySelectorAll('header.v-toolbar button, header.v-toolbar a.v-btn')]
      .filter((n) => !n.closest('[data-testid="notification-badge"]') && n.getAttribute('data-testid') !== 'notification-bell')
      .map((n) => n.getBoundingClientRect())
      .filter((o) => o.width > 0)
    const overlaps = others.some((o) => b.left < o.right && b.right > o.left && b.top < o.bottom && b.bottom > o.top)
    return {
      hits,
      overlaps,
      box: { left: b.left, right: b.right, top: b.top, bottom: b.bottom },
      vw,
      barTop: bar.top,
      barBottom: bar.bottom,
      bell: { w: bell.width, h: bell.height },
      scrollW: document.documentElement.scrollWidth,
    }
  })
  await page.screenshot({ path: `test-results/badge/${SHOT}/${shot}.png`, clip: { x: Math.max(0, r.vw - 260), y: 0, width: Math.min(260, r.vw), height: 72 } })
  expect(r.hits, 'badge edge points are the topmost element (not clipped)').toEqual([true, true, true, true])
  expect(r.box.left, 'badge inside left').toBeGreaterThanOrEqual(0)
  expect(r.box.right, 'badge inside right').toBeLessThanOrEqual(r.vw)
  expect(r.box.top, 'badge inside top').toBeGreaterThanOrEqual(r.barTop)
  expect(r.box.bottom, 'badge inside bar').toBeLessThanOrEqual(r.barBottom)
  expect(r.overlaps, 'badge overlaps a neighbouring button').toBe(false)
  expect(r.bell.w).toBeGreaterThanOrEqual(39.5)
  expect(r.bell.h).toBeGreaterThanOrEqual(39.5)
  expect(r.scrollW, 'no horizontal scroll').toBeLessThanOrEqual(r.vw)
}

const withUnread = async (page: Page, n: number) => {
  await page.route('**/api/v1/me/notifications/unread-count', (route) => route.fulfill({ json: { unread_count: n } }))
}

for (const [label, size] of [['desktop', { width: 1280, height: 720 }], ['phone', { width: 360, height: 740 }]] as const) {
  for (const scheme of ['light', 'dark'] as const) {
    test.describe(`unread badge, ${label}, ${scheme}`, () => {
      test.use({ viewport: size, colorScheme: scheme })
      test.skip(({ isMobile }) => isMobile, 'the viewport is set per test')

      for (const [n, text] of [[7, '7'], [250, '99+']] as const) {
        test(`agent panel, ${text}`, async ({ page }) => {
          await withUnread(page, n)
          await loginAs(page, 'staff', ADMIN)
          await page.goto('/agent/tickets')
          await expect(page.getByTestId('notification-count')).toHaveText(text)
          await expectBadgeVisible(page, `agent-${label}-${scheme}-${text.replace('+', 'plus')}`)
        })

        test(`portal, ${text}`, async ({ browser }) => {
          const ctx = await browser.newContext({ viewport: size, colorScheme: scheme, extraHTTPHeaders: clientIp() })
          const page = await ctx.newPage()
          await withUnread(page, n)
          const res = await page.request.post('/api/v1/auth/login', { data: { audience: 'customer', ...CUSTOMER } })
          test.skip(!res.ok(), 'needs a customer account (seed --demo, or E2E_CUSTOMER_EMAIL/PASSWORD)')
          await page.goto('/tickets')
          await expect(page.getByTestId('notification-count')).toHaveText(text)
          await expectBadgeVisible(page, `portal-${label}-${scheme}-${text.replace('+', 'plus')}`)
          await ctx.close()
        })
      }
    })
  }
}
