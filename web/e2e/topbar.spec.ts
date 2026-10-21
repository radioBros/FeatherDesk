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
