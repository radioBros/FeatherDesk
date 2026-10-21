// SPDX-License-Identifier: AGPL-3.0-only

/**
 * FE-032: axe (wcag2a + wcag2aa, serious and critical) on every page of the
 * portal, the public knowledge base and the agent panel, each with real data,
 * in the desktop and the mobile project. The catalogue in e2e/pages.ts must
 * name every route of the router (first test), so a new page is scanned or
 * the suite fails.
 */
import { test, expect, clientIp, a11y } from './fixtures'
import { MAILPIT } from './portal/helpers'
import { PAGES, coverage, open, seedWorld, type PageSpec, type World } from './pages'

test('every route of the SPA is in the page catalogue', () => {
  expect(coverage()).toEqual([])
})

// Timers never fire in a frame that runs no scripts, so axe.runPartial there never settles:
// the scan must still finish, check the iframe element and the frame's document (FE-047 previews).
test('the scan covers frames that run no scripts instead of hanging on them', async ({ page }) => {
  const frameDoc = '<!doctype html><html lang="en"><head><title>f</title></head><body><p style="color:#bbb">faint</p></body></html>'
  await page.route('http://frames.test/**', (route) =>
    route.request().url().endsWith('/csp')
      ? route.fulfill({ contentType: 'text/html', headers: { 'Content-Security-Policy': 'sandbox' }, body: frameDoc })
      : route.fulfill({
          contentType: 'text/html',
          body: `<!doctype html><html lang="en"><head><title>t</title></head><body><main><h1>x</h1>
            <iframe sandbox="" srcdoc="${frameDoc.replaceAll('"', '&quot;')}"></iframe>
            <iframe title="csp" src="/csp"></iframe></main></body></html>`,
        }),
  )
  await page.goto('http://frames.test/')
  const ids = (await a11y(page)).map((v) => v.id)
  expect(ids).toContain('frame-title') // the untitled sandboxed iframe element itself
  expect(ids).toContain('color-contrast (in frame about:srcdoc)')
  expect(ids).toContain('color-contrast (in frame http://frames.test/csp)')
})

test.describe('accessibility of every page', () => {
  test.skip(!MAILPIT, 'needs E2E_MAILPIT_URL: the signed-in portal pages need a verified customer and register/forgot need email')

  let world: World
  test.beforeAll(async ({ browser }, info) => {
    const ctx = await browser.newContext({ extraHTTPHeaders: clientIp() })
    // The project's device (viewport, touch, UA) for the visitor and the customer.
    const { viewport, userAgent, deviceScaleFactor, isMobile, hasTouch } = info.project.use
    world = await seedWorld(browser, await ctx.newPage(), { viewport, userAgent, deviceScaleFactor, isMobile, hasTouch })
    await ctx.close()
  })
  test.afterAll(async () => world?.close())

  for (const who of ['anon', 'customer', 'admin'] as const) {
    test(`${who} pages have no serious or critical axe violations`, async () => {
      test.setTimeout(240_000)
      const person = who === 'anon' ? world.anon : who === 'customer' ? world.customer! : world.admin
      const entries = Object.entries(PAGES).filter((e): e is [string, PageSpec] => !('skip' in e[1]) && e[1].who === who)
      expect(entries.length).toBeGreaterThan(0)
      const failures: Record<string, unknown> = {}
      for (const [name, spec] of entries) {
        try {
          await open(person.page, spec, world.seed)
        } catch (e) {
          failures[`${name} ${spec.path(world.seed)}`] = { notReady: String(e).split('\n')[0] } as never
          continue
        }
        const violations = await a11y(person.page)
        if (violations.length)
          failures[`${name} ${spec.path(world.seed)}`] = violations.map((v) => ({
            id: v.id,
            impact: v.impact,
            help: v.help,
            nodes: v.nodes.slice(0, 3).map((n) => `${n.target.join(' ')} :: ${n.html.slice(0, 160)}`),
          }))
      }
      expect(failures).toEqual({})
    })
  }
})
