// SPDX-License-Identifier: AGPL-3.0-only

/**
 * FE-003 / FE-058 against the built bundle: the first view ships English only,
 * and switching to French loads exactly one app chunk, one Vuetify locale
 * chunk and the French part of each message set the page uses — nothing else.
 */
import { test, expect } from './fixtures'
import fr from '../src/locales/fr.json' with { type: 'json' }
import frPortal from '../src/locales/portal/fr.json' with { type: 'json' }

const LOCALE_CHUNK = /\/assets\/(it|de|fr|es)-[\w-]+\.js$/

test('the first view loads no locale chunk; switching to French loads only French chunks', async ({ page }) => {
  const scripts: string[] = []
  page.on('request', (r) => {
    if (r.resourceType() === 'script') scripts.push(r.url())
  })
  await page.goto('/login')
  await expect(page.getByTestId('login-form')).toBeVisible()
  expect(scripts.filter((u) => LOCALE_CHUNK.test(u))).toEqual([])

  const before = scripts.length
  const bodies: string[] = []
  page.on('response', async (r) => {
    if (LOCALE_CHUNK.test(r.url())) bodies.push(await r.text())
  })
  await page.getByRole('button', { name: 'Language' }).click()
  await page.getByRole('option', { name: 'Français' }).or(page.getByText('Français', { exact: true })).first().click()
  await expect(page.getByTestId('login-kb-link')).toHaveText(fr.nav.help)
  await expect(page.locator('html')).toHaveAttribute('lang', 'fr')

  const loaded = scripts.slice(before)
  expect(loaded.length).toBeGreaterThanOrEqual(3)
  for (const u of loaded) expect(u, 'only French chunks load on the switch').toMatch(/\/assets\/fr-[\w-]+\.js$/)
  await expect.poll(() => bodies.length).toBe(loaded.length)
  // Exactly one app chunk, one Vuetify locale pack, one portal message pack.
  expect(bodies.filter((b) => b.includes(fr.app.cancel) && b.includes(fr.nav.help))).toHaveLength(1)
  expect(bodies.filter((b) => b.includes('dataIterator'))).toHaveLength(1)
  expect(bodies.filter((b) => b.includes(frPortal.portal.tickets.allStatuses))).toHaveLength(1)
})
