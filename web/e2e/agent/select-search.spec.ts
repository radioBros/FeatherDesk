// SPDX-License-Identifier: AGPL-3.0-only

/** Every select is AutocompleteSearch: opening one shows a search box that filters the options. */
import { test, expect, ADMIN } from '../fixtures'
import { signIn } from './helpers'

test.describe('select search', () => {
  test.skip(({ isMobile }) => isMobile, 'desktop filter bar')

  test('the status filter on the agent ticket list filters its options while typing', async ({ browser }) => {
    const admin = await signIn(browser, 'staff', ADMIN)
    const page = admin.page
    await page.goto('/agent/tickets')
    await page.getByTestId('filter-status').locator('.v-field').click()
    const search = page.getByTestId('autocomplete-search-input').locator('input')
    await expect(search).toBeFocused()
    const options = page.getByRole('option')
    expect(await options.count()).toBeGreaterThan(3)
    await search.fill('pend')
    await expect(options).toHaveCount(1)
    await expect(options.first()).toContainText(/pending/i)
    await search.fill('zzzz')
    await expect(options).toHaveCount(0)
    await admin.context.close()
  })

  // Regression: Vuetify moved focus onto the first list item after opening, so
  // real keystrokes went to the list type-ahead instead of the search box.
  // .fill() bypasses that, so type key by key like a person does.
  test('real keystrokes after opening land in the search box and filter', async ({ browser }) => {
    const admin = await signIn(browser, 'staff', ADMIN)
    const page = admin.page
    await page.goto('/agent/tickets')
    await page.getByTestId('filter-status').locator('.v-field').click()
    const search = page.getByTestId('autocomplete-search-input').locator('input')
    await page.waitForTimeout(300) // let the menu transition finish, as a user would
    await page.keyboard.type('pe', { delay: 80 })
    await expect(search).toHaveValue('pe')
    await expect(search).toBeFocused()
    const options = page.getByRole('option')
    await expect(options).toHaveCount(2)
    await page.keyboard.press('Backspace')
    await page.keyboard.press('Backspace')
    await page.keyboard.type('clo', { delay: 80 })
    await expect(search).toHaveValue('clo')
    await expect(options).toHaveCount(1)
    await expect(options.first()).toContainText(/closed/i)
    await admin.context.close()
  })
})
