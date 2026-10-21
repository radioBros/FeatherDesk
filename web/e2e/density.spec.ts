// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Input density: every field of the custom-fields grid (select, multiselect with chips, text,
 * textarea, date) renders at the same height, and the checkbox sits on the same vertical centre.
 * Also writes the screenshots of the owner's review to web/test-results/density/ (the phase,
 * "before" or "after", comes from DENSITY_PHASE; default "after").
 */
import { mkdirSync } from 'node:fs'
import { test, expect, ADMIN } from './fixtures'
import { apiFor, signIn, uid } from './agent/helpers'

const phase = process.env.DENSITY_PHASE ?? 'after'
const dir = 'test-results/density'
const opt = (value: string, label: string) => ({ value, label: { en: label } })

test.describe('input density', () => {
  test.skip(({ isMobile }) => isMobile, 'desktop grid')

  test('custom-fields grid, ticket filters and an admin form share one height', async ({ browser }) => {
    mkdirSync(dir, { recursive: true })
    const admin = await signIn(browser, 'staff', ADMIN)
    const page = admin.page
    const api = await apiFor(page)
    const id = uid().replace(/[^a-z0-9]/g, '')
    const k = (n: string) => `${n}_${id}`
    const defs = [
      { key: k('os'), label: { en: 'Operating system' }, type: 'select', options: [opt('mac', 'macOS'), opt('win', 'Windows'), opt('lin', 'Linux')] },
      { key: k('sys'), label: { en: 'Affected systems' }, type: 'multiselect', options: [opt('a', 'Billing'), opt('b', 'Payroll'), opt('c', 'Reporting'), opt('d', 'Mail')] },
      { key: k('ref'), label: { en: 'Reference' }, type: 'text' },
      { key: k('err'), label: { en: 'Error message' }, type: 'textarea' },
      { key: k('due'), label: { en: 'Due date' }, type: 'date' },
      { key: k('blk'), label: { en: 'Blocks my work' }, type: 'checkbox' },
    ]
    for (const d of defs) await api.post('/admin/custom-fields', { ...d, required: false, customer_visible: true, customer_editable: true })

    await page.goto('/agent/tickets')
    await page.getByTestId('filter-custom-toggle').click()
    const grid = page.getByTestId('cf-filters')
    await expect(grid).toBeVisible()

    // Three chips in the multiselect must not make it taller.
    await grid.getByTestId(`cf-${k('sys')}`).click()
    for (const n of ['Billing', 'Payroll', 'Reporting']) await page.getByRole('option', { name: n }).click()
    await page.keyboard.press('Escape')
    await grid.getByTestId(`cf-${k('err')}`).locator('textarea').fill('Line one\nLine two')
    await grid.getByTestId(`cf-${k('err')}`).locator('textarea').fill('Short')

    const h = async (key: string) => (await grid.getByTestId(`cf-${key}`).locator('.v-field').boundingBox())!.height
    const heights = {
      select: await h(k('os')),
      multiselect: await h(k('sys')),
      text: await h(k('ref')),
      textarea: await h(k('err')),
      date: await h(k('due')),
    }
    const base = heights.text
    for (const [name, v] of Object.entries(heights)) expect.soft(Math.abs(v - base), `${name} ${v}px vs text ${base}px`).toBeLessThanOrEqual(1)
    // Accessible hit target: a field is never smaller than 36 px.
    expect.soft(base).toBeGreaterThanOrEqual(36)

    // The checkbox row aligns to the same centre as the inputs.
    const cb = grid.getByTestId(`cf-${k('blk')}`)
    const root = (await cb.boundingBox())!
    const ctl = (await cb.locator('.v-selection-control').boundingBox())!
    expect.soft(Math.abs(root.y + root.height / 2 - (ctl.y + ctl.height / 2))).toBeLessThanOrEqual(1)
    const ref = (await grid.getByTestId(`cf-${k('ref')}`).boundingBox())!
    expect.soft(Math.abs(root.height - ref.height)).toBeLessThanOrEqual(1)

    await grid.screenshot({ path: `${dir}/custom-fields-grid-${phase}.png` })
    await page.getByTestId('ticket-table').waitFor()
    await page.screenshot({ path: `${dir}/ticket-list-filters-${phase}.png`, clip: { x: 0, y: 0, width: 1280, height: 520 } })

    // One admin form (a dialog with a select, text fields and a textarea).
    await page.goto('/agent/admin/teams')
    await page.getByTestId('team-new').click()
    await page.locator('.v-dialog .v-card').first().screenshot({ path: `${dir}/admin-form-${phase}.png` })
    const fields = page.locator('.v-dialog .v-field')
    const n = await fields.count()
    const hs: number[] = []
    for (let i = 0; i < n; i++) hs.push((await fields.nth(i).boundingBox())!.height)
    const smallest = Math.min(...hs)
    for (const v of hs) if (v < 80) expect.soft(Math.abs(v - smallest)).toBeLessThanOrEqual(1)
    await admin.context.close()
  })
})
