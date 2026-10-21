// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Two-column pages (TwoColumnLayout): the page scrolls with the LONGER column and the SHORTER one
 * sticks beside it; no column gets an internal scrollbar. On a phone the columns stack and nothing
 * sticks. Screenshots go to test-results/sticky/.
 */
import { test, expect } from '../fixtures'
import { seedTeam, signIn, uid } from './helpers'

async function longTicket(browser: import('@playwright/test').Browser) {
  const seed = await seedTeam(browser)
  const id = uid()
  const c = await seed.api.post('/agent/customers', { name: `Sticky ${id}`, email: `sticky-${id}@e2e.test` })
  const ticket = await seed.api.post(
    '/agent/tickets',
    { subject: `Sticky panel ${id}`, body_md: 'First message', team_id: seed.team.id, requester_id: c.id },
    { 'Idempotency-Key': `sticky-${id}` },
  )
  for (let i = 0; i < 24; i++) {
    await seed.api.post(
      `/agent/tickets/${ticket.id}/messages`,
      { kind: 'internal', body_md: `Reply ${i}\n\n${'Some longer text to make the thread tall. '.repeat(8)}` },
      { 'Idempotency-Key': `sticky-${id}-${i}` },
    )
  }
  return { seed, ticket }
}

const top = (page: import('@playwright/test').Page) =>
  page.getByTestId('properties').evaluate((el) => el.closest('.st-two__inner')!.getBoundingClientRect().top)

test.describe('sticky properties panel', () => {
  test('desktop: stays at the sticky offset while the thread scrolls', async ({ browser }) => {
    const { seed, ticket } = await longTicket(browser)
    const agent = await signIn(browser, 'staff', seed.l1, { viewport: { width: 1280, height: 800 } })
    const page = agent.page
    await page.goto(`/agent/tickets/${ticket.number}`)
    const props = page.getByTestId('properties')
    await expect(props).toBeVisible()
    await page.screenshot({ path: 'test-results/sticky/ticket-before-scroll.png' })

    const offset = await page.evaluate(() => parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--st-sticky-top')))
    const bar = await page.evaluate(() => document.querySelector('.v-app-bar')!.getBoundingClientRect().bottom)
    expect(offset).toBeGreaterThanOrEqual(bar)
    const initial = await top(page)
    expect(initial).toBeGreaterThan(offset - 1)
    // The long thread drives the page scroll; the shorter properties column is the sticky one.
    await expect(page.getByTestId('two-column')).toHaveAttribute('data-sticky', 'aside')
    // No internal scrollbars: neither column clips its content.
    for (const side of ['two-column-main', 'two-column-aside']) {
      const overflow = await page.getByTestId(side).evaluate((el) => getComputedStyle(el.firstElementChild!).overflowY)
      expect(overflow).toBe('visible')
    }

    await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight))
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(500)
    await page.screenshot({ path: 'test-results/sticky/ticket-after-scroll.png' })
    // Sticks under the header when it fits; when taller than the viewport it sticks by its
    // bottom edge (top = viewport − height − 16) so every part of it stays reachable.
    const expected = await page.getByTestId('properties').evaluate((el, off) => {
      const inner = el.closest('.st-two__inner') as HTMLElement
      return Math.min(off, window.innerHeight - inner.offsetHeight - 16)
    }, offset)
    expect(Math.abs((await top(page)) - expected)).toBeLessThanOrEqual(1.5)
    await expect(props).toBeInViewport()
    const innerBottom = await page.getByTestId('properties').evaluate((el) => el.closest('.st-two__inner')!.getBoundingClientRect().bottom)
    expect(innerBottom).toBeLessThanOrEqual(800)
    await agent.context.close()
    await seed.admin.context.close()
  })

  test('mobile: the panel is stacked and not sticky', async ({ browser }) => {
    const { seed, ticket } = await longTicket(browser)
    const agent = await signIn(browser, 'staff', seed.l1, { viewport: { width: 390, height: 800 } })
    const page = agent.page
    await page.goto(`/agent/tickets/${ticket.number}`)
    await expect(page.getByTestId('properties')).toBeAttached()
    const aside = page.getByTestId('two-column-aside')
    await expect(page.getByTestId('two-column')).toHaveAttribute('data-sticky', 'none')
    expect(await aside.locator('.st-two__inner').evaluate((el) => getComputedStyle(el).position)).toBe('static')
    await page.screenshot({ path: 'test-results/sticky/ticket-mobile.png' })
    // Not pinned: its page position (top + scrollY) is the same whatever the scroll.
    const pageTop = () => aside.evaluate((el) => el.getBoundingClientRect().top + window.scrollY)
    const before = await pageTop()
    await page.evaluate(() => window.scrollTo(0, 400))
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(300)
    expect(Math.abs((await pageTop()) - before)).toBeLessThanOrEqual(1.5)
    await agent.context.close()
    await seed.admin.context.close()
  })
})
