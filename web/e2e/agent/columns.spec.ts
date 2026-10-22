// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Column visibility (useTableColumns): toggling a column off on the agent ticket list survives a reload,
 * the locked columns cannot be toggled, and "Reset to default" brings the column back.
 */
import { test, expect, ADMIN } from '../fixtures'
import { apiFor, signIn, uid } from './helpers'

test.describe('table columns', () => {
  test.skip(({ isMobile }) => isMobile, 'desktop table')

  test('a hidden column stays hidden after a reload; reset restores it', async ({ browser }) => {
    const admin = await signIn(browser, 'staff', ADMIN)
    const page = admin.page
    const api = await apiFor(page)
    const id = uid()
    await api.post('/agent/tickets', { subject: `Cols ${id}`, body_md: 'x', requester_email: `cols-${id}@e2e.test` }, { 'Idempotency-Key': `e2e-${id}` })

    await page.goto('/agent/tickets')
    const table = page.getByTestId('ticket-table')
    await expect(table.getByRole('columnheader', { name: 'Assignee' })).toBeVisible()

    await page.getByTestId('columns-menu').click()
    await expect(page.getByTestId('column-toggle-subject').locator('input')).toBeDisabled()
    await expect(page.getByTestId('column-toggle-number').locator('input')).toBeDisabled()
    await page.getByTestId('column-toggle-assignee').locator('input').uncheck()
    await expect(table.getByRole('columnheader', { name: 'Assignee' })).toHaveCount(0)

    await page.reload()
    await expect(table.getByRole('columnheader', { name: 'Subject' })).toBeVisible()
    await expect(table.getByRole('columnheader', { name: 'Assignee' })).toHaveCount(0)

    await page.getByTestId('columns-menu').click()
    await page.getByTestId('columns-reset').click()
    await expect(table.getByRole('columnheader', { name: 'Assignee' })).toBeVisible()
    await admin.context.close()
  })

  test('ticket list: columns reorder from the menu (drag and keys), resize, and the select column stays pinned', async ({ browser }) => {
    const admin = await signIn(browser, 'staff', ADMIN)
    const page = admin.page
    const api = await apiFor(page)
    const id = uid()
    await api.post('/agent/tickets', { subject: `Order ${id}`, body_md: 'x', requester_email: `order-${id}@e2e.test` }, { 'Idempotency-Key': `e2e-${id}` })
    await page.setViewportSize({ width: 1280, height: 1200 }) // the whole Columns list on screen for the drag
    await page.goto('/agent/tickets')
    const table = page.getByTestId('ticket-table')
    const order = () => table.locator('thead [data-resize-col]').evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.resizeCol))
    await expect(table.locator('thead [data-resize-col="status"]')).toBeVisible()
    const initial = await order()
    expect(initial.indexOf('status')).toBeGreaterThan(initial.indexOf('requester'))

    // The selection column is pinned (sticky) at the start.
    const sel = table.locator('thead th').first()
    await expect(sel).toHaveClass(/v-data-table__th--fixed/)
    expect(await sel.evaluate((e) => getComputedStyle(e).position)).toBe('sticky')

    // Keyboard: Status one place up, before Requester.
    await page.getByTestId('columns-menu').click()
    await page.getByTestId('column-drag-status').focus()
    await page.keyboard.press('ArrowUp')
    await expect(page.getByTestId('column-drag-status')).toBeFocused()
    await expect.poll(async () => { const o = await order(); return o.indexOf('status') < o.indexOf('requester') }).toBe(true)

    // Pointer: drag Due onto the first item (hover waits for the menu to stop animating).
    await page.getByTestId('column-drag-due').hover()
    const from = await page.getByTestId('column-drag-due').boundingBox()
    const to = await page.locator('[data-col-item="number"]').boundingBox()
    await page.mouse.move(from!.x + from!.width / 2, from!.y + from!.height / 2)
    await page.mouse.down()
    await page.mouse.move(to!.x + 20, to!.y + to!.height / 2, { steps: 8 })
    await page.mouse.up()
    await expect.poll(async () => (await order())[0]).toBe('due')
    await page.keyboard.press('Escape')

    // Resize Subject by dragging its header edge.
    const subjectTh = table.locator('thead th', { has: page.locator('[data-resize-col="subject"]') })
    const before = (await subjectTh.boundingBox())!.width
    const edge = (await table.locator('[data-resize-col="subject"]').boundingBox())!
    await page.mouse.move(edge.x + edge.width / 2, edge.y + edge.height / 2)
    await page.mouse.down()
    await page.mouse.move(edge.x + edge.width / 2 + 120, edge.y + edge.height / 2, { steps: 6 })
    await page.mouse.up()
    await expect.poll(async () => Math.round((await subjectTh.boundingBox())!.width - before)).toBeGreaterThanOrEqual(115)

    // Order and width survive a reload; reset restores both.
    await page.reload()
    await expect.poll(async () => (await order())[0]).toBe('due')
    expect(Math.round((await subjectTh.boundingBox())!.width - before)).toBeGreaterThanOrEqual(115)
    await page.getByTestId('columns-menu').click()
    await page.getByTestId('columns-reset').click()
    await expect.poll(order).toEqual(initial)
    await admin.context.close()
  })

  test('plain tables reorder and resize the same way', async ({ browser }) => {
    const admin = await signIn(browser, 'staff', ADMIN)
    const page = admin.page
    await page.goto('/agent/admin/staff')
    const table = page.getByTestId('staff-table')
    const head = () => table.locator('thead th[data-col]').evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.col))
    const row = () => table.getByTestId('staff-row').first().locator('td[data-col]').evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.col))
    await expect(table.getByTestId('staff-row').first()).toBeVisible()
    const initial = await head()
    expect(initial[0]).toBe('name')

    await page.getByTestId('columns-menu').click()
    await page.getByTestId('column-drag-role').focus()
    await page.keyboard.press('ArrowUp')
    await page.keyboard.press('Escape')
    await expect.poll(head).toEqual(['role', 'name', ...initial.slice(2)])
    expect(await row()).toEqual(await head())

    const name = table.locator('thead th[data-col="name"]')
    const before = (await name.boundingBox())!.width
    await table.locator('[data-resize-col="name"]').focus()
    await page.keyboard.press('ArrowRight')
    await page.keyboard.press('ArrowRight')
    // Two 16 px steps; the first resize rounds the measured width, so ±1 px.
    const grown = async () => Math.abs((await name.boundingBox())!.width - before - 32) <= 1
    await expect.poll(grown).toBe(true)
    expect(await table.locator('table').evaluate((e) => getComputedStyle(e).tableLayout)).toBe('fixed')

    await page.reload()
    await expect.poll(head).toEqual(['role', 'name', ...initial.slice(2)])
    await expect.poll(grown).toBe(true)
    await page.getByTestId('columns-menu').click()
    await page.getByTestId('columns-reset').click()
    await expect.poll(head).toEqual(initial)
    expect(await table.locator('table').evaluate((e) => getComputedStyle(e).tableLayout)).toBe('auto')
    await admin.context.close()
  })
})
