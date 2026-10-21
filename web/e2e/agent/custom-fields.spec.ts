// SPDX-License-Identifier: AGPL-3.0-only

/**
 * FE-053/FE-054 E2E: custom field round-trip — the admin defines a select
 * field in the UI, an agent sets it on a ticket in the properties panel, the
 * value survives a reload, and the list filters on `cf.<key>`.
 * Needs the full backend (runs during integration).
 */
import { test, expect, ADMIN } from '../fixtures'
import { apiFor, signIn, uid } from './helpers'

test.describe('custom field round-trip', () => {
  test.skip(({ isMobile }) => isMobile, 'admin pages are covered on desktop')

  test('define → set on a ticket → reload → filter', async ({ browser }) => {
    const admin = await signIn(browser, 'staff', ADMIN)
    const page = admin.page
    // Keys follow the API pattern ^[a-z][a-z0-9_]{0,62}$ (no dashes).
    const key = `plan_${uid()}`

    // 1. Define the field in the admin UI.
    await page.goto('/agent/admin/custom-fields')
    await page.getByTestId('cf-new').click()
    await page.getByTestId('cf-key').locator('input').fill(key)
    await page.getByTestId('cf-label-en').locator('input').fill('Plan')
    await page.getByTestId('cf-type').click()
    await page.getByRole('option', { name: 'Single choice', exact: true }).click()
    if (!(await page.getByTestId('cf-option-value-0').count())) await page.getByTestId('cf-option-add').click()
    await page.getByTestId('cf-option-value-0').locator('input').fill('gold')
    await page.getByTestId('cf-option-label-0-en').locator('input').fill('Gold')
    const created = page.waitForResponse((r) => r.url().endsWith('/api/v1/admin/custom-fields') && r.request().method() === 'POST')
    await page.getByTestId('cf-save').click()
    expect((await created).status()).toBe(201)
    await expect(page.getByTestId('cf-row').filter({ hasText: key })).toBeVisible()

    // 2. Set it on a ticket from the properties panel.
    const api = await apiFor(page)
    const id = uid()
    const ticket = await api.post('/agent/tickets', { subject: `CF ${id}`, body_md: 'x', requester_email: `cf-${id}@e2e.test` }, { 'Idempotency-Key': `e2e-${id}` })
    await page.goto(`/agent/tickets/${ticket.number}`)
    await page.getByTestId(`cf-${key}`).click()
    await page.getByRole('option', { name: 'Gold' }).click()
    const saved = page.waitForResponse((r) => r.url().endsWith(`/api/v1/agent/tickets/${ticket.id}`) && r.request().method() === 'PATCH')
    await page.getByTestId('custom-fields-save').click()
    const res = await saved
    expect(res.status()).toBe(200)
    expect(JSON.parse(res.request().postData() ?? '{}')).toMatchObject({ custom_fields: { [key]: 'gold' } })

    // 3. Survives a reload.
    await page.reload()
    await expect(page.getByTestId(`cf-${key}`)).toContainText('Gold')

    // 4. The list filters on cf.<key> (server-side).
    const listed = page.waitForResponse((r) => r.url().includes('/api/v1/agent/tickets?') && r.url().includes(`cf.${key}=gold`))
    await page.goto(`/agent/tickets?cf.${key}=gold`)
    await listed
    await expect(page.getByTestId('ticket-table')).toContainText(`CF ${id}`)

    await admin.context.close()
  })
})
