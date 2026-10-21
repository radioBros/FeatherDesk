// SPDX-License-Identifier: AGPL-3.0-only

/**
 * FE-032: the customer-facing pages (portal, signed out and in, and the public
 * knowledge base) fit a 360 px wide phone: no horizontal scroll. Runs in the
 * mobile project (touch, mobile UA) with the viewport narrowed from the
 * Pixel 7's 412 px to 360 px.
 */
import { test, expect, clientIp } from './fixtures'
import { MAILPIT } from './portal/helpers'
import { PAGES, open, seedWorld, type PageSpec, type World } from './pages'

const WIDTH = 360

test.describe('portal and knowledge base at 360 px', () => {
  test.skip(({ isMobile }) => !isMobile, 'mobile project only')
  test.skip(!MAILPIT, 'needs E2E_MAILPIT_URL for a verified customer')

  let world: World
  test.beforeAll(async ({ browser }, info) => {
    const ctx = await browser.newContext({ extraHTTPHeaders: clientIp() })
    // The project's device (viewport, touch, UA) for the visitor and the customer.
    const { viewport, userAgent, deviceScaleFactor, isMobile, hasTouch } = info.project.use
    world = await seedWorld(browser, await ctx.newPage(), { viewport, userAgent, deviceScaleFactor, isMobile, hasTouch })
    await ctx.close()
  })
  test.afterAll(async () => world?.close())

  for (const who of ['anon', 'customer'] as const) {
    test(`${who}: no horizontal scroll on any portal or help-center page`, async () => {
      test.setTimeout(180_000)
      const person = who === 'anon' ? world.anon : world.customer!
      await person.page.setViewportSize({ width: WIDTH, height: 780 })
      const entries = Object.entries(PAGES).filter(
        (e): e is [string, PageSpec] => !('skip' in e[1]) && e[1].who === who && !e[1].path(world.seed).startsWith('/agent'),
      )
      expect(entries.length).toBeGreaterThan(0)
      const wide: Record<string, { scrollWidth: number; culprits: string[] }> = {}
      for (const [name, spec] of entries) {
        try {
          await open(person.page, spec, world.seed)
        } catch (e) {
          wide[`${name} ${spec.path(world.seed)}`] = { notReady: String(e).split('\n')[0] } as never
          continue
        }
        const m = await person.page.evaluate((w) => {
          const scrollWidth = document.scrollingElement!.scrollWidth
          const culprits = [...document.querySelectorAll<HTMLElement>('body *')]
            .filter((el) => el.getBoundingClientRect().right > w + 0.5 && getComputedStyle(el).position !== 'fixed')
            .slice(0, 5)
            .map((el) => `${el.tagName.toLowerCase()}${el.id ? '#' + el.id : ''}.${[...el.classList].slice(0, 3).join('.')}`)
          return { scrollWidth, culprits }
        }, WIDTH)
        if (m.scrollWidth > WIDTH) wide[`${name} ${spec.path(world.seed)}`] = m
      }
      expect(wide).toEqual({})
    })
  }
})
