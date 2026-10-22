// SPDX-License-Identifier: AGPL-3.0-only

/**
 * GDPR tooling E2E (D-19 §C, FE-089):
 *  - a customer requests an export of their data on "My data", waits until it
 *    is ready and downloads the ZIP;
 *  - an admin erases a customer from the customer detail view, and the
 *    customer's name disappears from the agent ticket list.
 * The export test needs a customer account (E2E_CUSTOMER_* or E2E_MAILPIT_URL);
 * the erase test runs against the default E2E server.
 */
import { readFile } from 'node:fs/promises'
import { test, expect } from './fixtures'
import { customerAccount, seedTeam, signIn, uid } from './agent/helpers'

test.describe('privacy (GDPR)', () => {
  test('a customer requests an export and downloads the ZIP', async ({ browser, page }) => {
    const creds = await customerAccount(page)
    test.skip(!creds, 'needs E2E_CUSTOMER_EMAIL/E2E_CUSTOMER_PASSWORD or E2E_MAILPIT_URL')
    const customer = await signIn(browser, 'customer', creds!)
    const p = customer.page
    await p.goto('/profile')
    await p.getByTestId('profile-privacy-link').click()
    await expect(p).toHaveURL(/\/profile\/data$/)

    // A previous project run (chromium/mobile share the account) may have an export already pending.
    const request = p.getByTestId('export-request')
    await expect(request.or(p.getByTestId('export-pending')).first()).toBeVisible()
    if (await request.isEnabled()) await request.click()

    // The export is an async job: reload until the download link appears.
    const download = p.getByTestId('privacy-download').first()
    await expect(async () => {
      await p.getByTestId('my-data-refresh').click()
      await expect(download).toBeVisible({ timeout: 1_000 })
    }).toPass({ timeout: 30_000, intervals: [500, 1_000, 2_000] })

    const [file] = await Promise.all([p.waitForEvent('download'), download.click()])
    expect(file.suggestedFilename()).toMatch(/\.zip$/)
    const bytes = await readFile((await file.path())!)
    expect(bytes.subarray(0, 2).toString('latin1')).toBe('PK')
    await customer.context.close()
  })

  test('an admin erases a customer and their name leaves the ticket list', async ({ browser }) => {
    const seed = await seedTeam(browser)
    const id = uid()
    const name = `Erin Erasable ${id}`
    const ticket = await seed.api.post(
      '/agent/tickets',
      { subject: `Privacy erase ${id}`, body_md: 'Please call me back.', requester_email: `erin-${id}@e2e.test`, requester_name: name, team_id: seed.team.id },
      { 'Idempotency-Key': `e2e-privacy-${id}` },
    )
    const admin = seed.admin.page
    const list = async () => {
      await admin.goto('/agent/tickets')
      await admin.getByTestId('filter-q').locator('input').fill(id)
      // Applied on Enter; the virtual table only renders the rows in view.
      await admin.getByTestId('filter-q').locator('input').press('Enter')
      return admin.getByTestId('ticket-row').filter({ hasText: `Privacy erase ${id}` })
    }
    await expect(async () => {
      await expect((await list()).first()).toContainText(name, { timeout: 2_000 })
    }).toPass({ timeout: 20_000 })

    await admin.goto(`/agent/customers/${ticket.requester.id}`)
    await expect(admin.getByTestId('privacy-actions')).toBeVisible()
    await admin.getByTestId('privacy-erase').click()
    await admin.getByTestId('erase-mode').click()
    await admin.getByRole('option', { name: 'Anonymize the account' }).click()
    await admin.getByTestId('erase-confirm').locator('input').check()
    await admin.getByTestId('erase-submit').click()
    await expect(admin.getByTestId('erase-form')).toBeHidden()
    await expect(admin.getByTestId('privacy-user-requests')).toContainText('Completed')

    const row = (await list()).first()
    await expect(row).toBeVisible({ timeout: 15_000 })
    await expect(row).toContainText('Deleted user')
    await expect(row).not.toContainText(name)
    expect(await admin.content()).not.toContain(name)
    await seed.admin.context.close()
  })
})
