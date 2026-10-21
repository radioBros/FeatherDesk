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
})
