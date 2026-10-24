// SPDX-License-Identifier: AGPL-3.0-only

/**
 * The rich-text editor with a real keyboard: fast typing keeps every key (the
 * v-model echo must not reset the document), the toolbar formats the
 * selection and shows it as pressed, list shortcuts work, and the Markdown
 * that gets stored is what the formatting says.
 */
import { test, expect, ADMIN, loginAs } from './fixtures'

test('typing, bold from the toolbar and a list are stored as Markdown', async ({ page }) => {
  await loginAs(page, 'staff', ADMIN)
  await page.goto('/agent/tickets/new')
  const editor = page.locator('[contenteditable="true"]').first()
  await editor.click()
  const bold = page.getByTestId('md-tool-bold').first()
  await bold.click()
  await expect(bold).toHaveAttribute('aria-pressed', 'true')
  await expect(editor).toBeFocused()
  await page.keyboard.type('Hello world, typed quickly with every key kept')
  await bold.click()
  await expect(bold).toHaveAttribute('aria-pressed', 'false')
  await expect(editor).toBeFocused()
  await page.keyboard.type(' and plain text')
  await page.keyboard.press('Enter')
  await page.keyboard.type('- item one')
  await page.keyboard.press('Enter')
  await page.keyboard.type('item two')

  await page.getByTestId('md-markdown-toggle').first().click()
  await expect(page.getByTestId('md-source').first()).toHaveValue('**Hello world, typed quickly with every key kept** and plain text\n\n- item one\n- item two')
})
