// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Header sorting of a cursor list (DataTable server-sort): the order holds across pages loaded by
 * scrolling, and a resize drag on the header edge does not touch the sort. Runs alone, after the
 * other projects (playwright.config.ts): the virtual table measures its rows on animation frames,
 * which a fully loaded parallel run can starve, and the other specs' ticket events would refetch
 * the list while it is being scrolled.
 */
import { test, expect, ADMIN } from '../fixtures'
import { apiFor, signIn, uid } from './helpers'

test.describe('table sorting', () => {
  test.skip(({ isMobile }) => isMobile, 'desktop table')

  test('ticket list: a header click sorts on the server, and the order holds across pages loaded by scrolling', async ({ browser }) => {
    test.setTimeout(120_000) // 60 tickets through the API first
    const admin = await signIn(browser, 'staff', ADMIN)
    const page = admin.page
    const api = await apiFor(page)
    const id = uid()
    const prios = ['low', 'normal', 'high', 'urgent']
    // More than one page (the list asks for 50).
    for (let i = 0; i < 60; i++) {
      await api.post('/agent/tickets', { subject: `Sorted ${id} ${i}`, body_md: 'x', requester_email: `sort-${id}@e2e.test`, priority: prios[i % 4] }, { 'Idempotency-Key': `e2e-${id}-${i}` })
    }
    // The search index is updated in the background: wait until it has them all.
    await expect.poll(async () => ((await api.get(`/agent/tickets?q=${id}&limit=100`)) as { items: unknown[] }).items.length, { timeout: 30_000 }).toBe(60)
    await page.goto(`/agent/tickets?view=all&q=${id}`)
    const table = page.getByTestId('ticket-table')
    await expect(table.locator('tbody [data-testid="ticket-row"]').first()).toBeVisible()

    const header = table.locator('thead th[data-col="priority"]')
    await header.click()
    await expect(page).toHaveURL(/sort=priority/)
    await header.click()
    await expect(page).toHaveURL(/sort=-priority/)
    await expect(header).toHaveAttribute('aria-sort', 'descending')

    // Scroll the table body to its end until every page is in, noting each row's priority as it renders.
    const seen = new Map<string, string>()
    const body = table.locator('.v-table__wrapper')
    await expect.poll(async () => {
      const rows = await table.locator('tbody [data-testid="ticket-row"]').evaluateAll((trs) =>
        trs.map((tr) => [tr.querySelector('[data-col="subject"]')?.textContent?.trim() ?? '', tr.querySelector('[data-col="priority"]')?.textContent?.trim() ?? '']),
      )
      for (const [subject, prio] of rows) seen.set(subject, prio)
      await body.evaluate((el) => (el.scrollTop += el.clientHeight))
      return seen.size
    }, { timeout: 30_000 }).toBe(60)

    const rank: Record<string, number> = { Urgent: 3, High: 2, Normal: 1, Low: 0 }
    const order = [...seen.values()].map((p) => rank[p])
    expect(order.every((r) => r !== undefined)).toBe(true)
    expect(order).toEqual([...order].sort((a, b) => b! - a!))

    // A drag on the header edge resizes without touching the sort.
    const edge = (await table.locator('[data-resize-col="priority"]').boundingBox())!
    await page.mouse.move(edge.x + edge.width / 2, edge.y + edge.height / 2)
    await page.mouse.down()
    await page.mouse.move(edge.x - 30, edge.y + edge.height / 2, { steps: 5 })
    await page.mouse.up()
    await expect(page).toHaveURL(/sort=-priority/)
    await page.getByTestId('columns-menu').click()
    await page.getByTestId('columns-reset').click()
    await admin.context.close()
  })
})
