// SPDX-License-Identifier: AGPL-3.0-only

/**
 * FE-052 end to end against the real API: the KB category admin (create a
 * tree, edit, reorder, delete), the article editor with one tab per locale
 * (a translation added, then removed), and Publish / Unpublish / Archive /
 * Delete shown exactly when the article's `allowed_actions` from the server
 * lists them, for a plain agent and for a supervisor. The public /kb follows
 * each status change.
 */
import { test, expect, clientIp, fillEditor, type Credentials } from '../fixtures'
import type { Page } from '@playwright/test'
import { apiFor, seedTeam, signIn, tableRow, uid } from './helpers'

const CONTROLS = { publish: 'kb-publish', unpublish: 'kb-unpublish', archive: 'kb-archive', delete: 'kb-delete' } as const

/** Every editor button is present iff the server's allowed_actions for this user lists it. */
async function expectControlsMatchServer(page: Page, articleId: string) {
  const api = await apiFor(page)
  const article = await api.get(`/agent/kb/articles/${articleId}`)
  const allowed: string[] = article.allowed_actions
  for (const [action, testid] of Object.entries(CONTROLS)) {
    await expect(page.getByTestId(testid), `${action} ⇔ allowed_actions ${JSON.stringify(allowed)}`).toHaveCount(allowed.includes(action) ? 1 : 0)
  }
  await expect(page.getByTestId('kb-save')).toHaveCount(allowed.includes('edit') ? 1 : 0)
  return allowed
}

async function pickOption(page: Page, testid: string, name: string | RegExp) {
  await page.getByTestId(testid).click()
  await page.getByRole('option', { name }).click()
}

test.describe('KB editor and categories', () => {
  test.skip(({ isMobile }) => isMobile, 'staff editor flow is covered on desktop')

  test('categories CRUD + order, article translations, and publish/archive driven by allowed_actions', async ({ browser }) => {
    test.setTimeout(90_000)
    const seed = await seedTeam(browser)
    const id = uid()
    const supervisor: Credentials = { email: `sup-${id}@e2e.test`, password: `e2e-supervisor-password-${id}` }
    await seed.api.post('/admin/staff', { ...supervisor, name: `Supervisor ${id}`, role: 'supervisor', tier: 3, team_ids: [seed.team.id] })

    // --- Category admin (admin UI) -------------------------------------------------------------
    const admin = seed.admin.page
    await admin.goto('/agent/admin/kb-categories')
    const createCategory = async (title: string, slug: string, parent?: string) => {
      await admin.getByTestId('kbcat-new').click()
      await admin.getByTestId('kbcat-title-en').locator('input').fill(title)
      await admin.getByTestId('kbcat-slug').locator('input').fill(slug)
      if (parent) await pickOption(admin, 'kbcat-parent', new RegExp(parent))
      await pickOption(admin, 'kbcat-visibility', 'Public')
      await admin.getByTestId('kbcat-save').click()
      await expect(admin.getByTestId('kb-category-form')).toHaveCount(0)
      await tableRow(admin.getByTestId('kbcat-table'), 'kbcat-row', title)
    }
    // Empty submit sends nothing (FE-005).
    let posted = false
    admin.on('request', (r) => {
      if (r.method() === 'POST' && r.url().endsWith('/api/v1/admin/kb/categories')) posted = true
    })
    await admin.getByTestId('kbcat-new').click()
    await admin.getByTestId('kbcat-save').click()
    await expect(admin.getByTestId('kb-category-form')).toContainText('This field is required')
    expect(posted).toBe(false)
    await admin.keyboard.press('Escape')

    await createCategory(`Guides ${id}`, `guides-${id}`)
    await createCategory(`Printers ${id}`, `printers-${id}`, `Guides ${id}`)
    await createCategory(`Scanners ${id}`, `scanners-${id}`, `Guides ${id}`)
    await createCategory(`Obsolete ${id}`, `obsolete-${id}`)

    const cats = async () => ((await seed.api.get('/admin/kb/categories')) as { id: string; slug: string; parent_id?: string | null; sort?: number; title: Record<string, string> }[])
    let all = await cats()
    const guides = all.find((c) => c.slug === `guides-${id}`)!
    expect(all.find((c) => c.slug === `printers-${id}`)!.parent_id).toBe(guides.id)
    expect(all.find((c) => c.slug === `scanners-${id}`)!.parent_id).toBe(guides.id)

    // Reorder siblings: Scanners moves above Printers.
    const sortOf = (list: typeof all, slug: string) => list.find((c) => c.slug === slug)!.sort ?? 0
    const scannersRow = await tableRow(admin.getByTestId('kbcat-table'), 'kbcat-row', `Scanners ${id}`)
    await scannersRow.getByTestId('kbcat-up').click()
    await expect.poll(async () => {
      const now = await cats()
      return sortOf(now, `scanners-${id}`) < sortOf(now, `printers-${id}`)
    }).toBe(true)

    // Edit a title, then delete the unused category.
    await (await tableRow(admin.getByTestId('kbcat-table'), 'kbcat-row', `Printers ${id}`)).getByRole('button', { name: 'Edit' }).click()
    await admin.getByTestId('kbcat-title-en').locator('input').fill(`Printers and copiers ${id}`)
    await admin.getByTestId('kbcat-save').click()
    await tableRow(admin.getByTestId('kbcat-table'), 'kbcat-row', `Printers and copiers ${id}`)
    await (await tableRow(admin.getByTestId('kbcat-table'), 'kbcat-row', `Obsolete ${id}`)).getByTestId('kbcat-delete').click()
    await admin.getByTestId('confirm-ok').click()
    await expect(admin.getByTestId('kbcat-table')).not.toContainText(`Obsolete ${id}`)
    all = await cats()
    expect(all.some((c) => c.slug === `obsolete-${id}`)).toBe(false)

    // --- A plain agent writes a draft in two languages ------------------------------------------
    const agent = await signIn(browser, 'staff', seed.l1)
    await agent.page.goto('/agent/kb/articles/new')
    await pickOption(agent.page, 'kb-category', new RegExp(`Printers and copiers ${id}`))
    await agent.page.getByTestId('kb-title-en').locator('input').fill(`Clear a paper jam ${id}`)
    await fillEditor(agent.page.getByTestId('md-input').first(), 'Open **tray 2** and pull the sheet out.')
    await agent.page.getByTestId('kb-tab-de').click()
    await expect(agent.page.getByTestId('kb-tab-de').getByTestId('kb-missing')).toHaveCount(1)
    await agent.page.getByTestId('kb-title-de').locator('input').fill(`Papierstau beheben ${id}`)
    await fillEditor(agent.page.locator('.v-window-item--active').getByTestId('md-input'), 'Öffnen Sie **Fach 2** und ziehen Sie das Blatt heraus.')
    await expect(agent.page.getByTestId('kb-tab-de').getByTestId('kb-missing')).toHaveCount(0)
    await agent.page.getByTestId('kb-save').click()
    await expect(agent.page).toHaveURL(/\/agent\/kb\/articles\/[0-9a-f-]{36}$/)
    const articleId = agent.page.url().split('/').pop()!
    await expect(agent.page.getByTestId('kb-status')).toHaveText('Draft')

    const draft = await (await apiFor(agent.page)).get(`/agent/kb/articles/${articleId}`)
    expect(draft.translations.map((t: { locale: string }) => t.locale).sort()).toEqual(['de', 'en'])
    const slug: string = draft.slug
    // The agent may edit but not publish or archive (permissions §3, decided by the server).
    const agentAllowed = await expectControlsMatchServer(agent.page, articleId)
    expect(agentAllowed).toContain('edit')
    expect(agentAllowed).not.toContain('publish')
    expect(agentAllowed).not.toContain('archive')

    // Not on the public KB while it is a draft.
    const visitor = await browser.newContext({ extraHTTPHeaders: clientIp() })
    const anon = await visitor.newPage()
    expect((await anon.request.get(`/api/v1/kb/articles/${slug}`)).status()).toBe(404)

    // --- The supervisor publishes, removes the German translation, unpublishes, archives -------
    const sup = await signIn(browser, 'staff', supervisor)
    await sup.page.goto(`/agent/kb/articles/${articleId}`)
    await expect(sup.page.getByTestId('kb-status')).toHaveText('Draft')
    const supDraft = await expectControlsMatchServer(sup.page, articleId)
    expect(supDraft).toContain('publish')

    await sup.page.getByTestId('kb-publish').click()
    await expect(sup.page.getByTestId('kb-status')).toHaveText('Published')
    const supPublished = await expectControlsMatchServer(sup.page, articleId)
    expect(supPublished).toContain('unpublish')
    expect(supPublished).not.toContain('publish')

    await anon.goto(`/kb/a/${slug}`)
    await expect(anon.getByTestId('kb-article-title')).toHaveText(`Clear a paper jam ${id}`)
    await expect(anon.getByTestId('kb-article-body')).toContainText('tray 2')
    const de = await (await anon.request.get(`/api/v1/kb/articles/${slug}?locale=de`)).json()
    expect(de.title).toBe(`Papierstau beheben ${id}`)

    await sup.page.getByTestId('kb-tab-de').click()
    await sup.page.getByTestId('kb-remove-de').click()
    await sup.page.getByTestId('confirm-ok').click()
    await expect(sup.page.getByTestId('kb-tab-de').getByTestId('kb-missing')).toHaveCount(1)
    await expect
      .poll(async () => ((await (await apiFor(sup.page)).get(`/agent/kb/articles/${articleId}`)).translations as { locale: string }[]).map((t) => t.locale))
      .toEqual(['en'])
    const deFallback = await (await anon.request.get(`/api/v1/kb/articles/${slug}?locale=de`)).json()
    expect(deFallback.title).toBe(`Clear a paper jam ${id}`)

    await sup.page.getByTestId('kb-unpublish').click()
    await expect(sup.page.getByTestId('kb-status')).toHaveText('Draft')
    await expectControlsMatchServer(sup.page, articleId)
    expect((await anon.request.get(`/api/v1/kb/articles/${slug}`)).status()).toBe(404)

    await sup.page.getByTestId('kb-publish').click()
    await expect(sup.page.getByTestId('kb-status')).toHaveText('Published')
    await sup.page.getByTestId('kb-archive').click()
    await expect(sup.page.getByTestId('kb-status')).toHaveText('Archived')
    const supArchived = await expectControlsMatchServer(sup.page, articleId)
    expect(supArchived).not.toContain('archive')
    // An archived article answers 410 (the earlier 200 may sit in this browser's cache for
    // max-age=300, so the page check uses a fresh visitor).
    expect((await anon.request.get(`/api/v1/kb/articles/${slug}`)).status()).toBe(410)
    const late = await browser.newContext({ extraHTTPHeaders: clientIp() })
    const latePage = await late.newPage()
    await latePage.goto(`/kb/a/${slug}`)
    await expect(latePage.getByTestId('kb-article-error')).toBeVisible()
    await late.close()

    // The agent, on the archived article, still gets no publish/archive control.
    await agent.page.goto(`/agent/kb/articles/${articleId}`)
    await expect(agent.page.getByTestId('kb-status')).toHaveText('Archived')
    await expectControlsMatchServer(agent.page, articleId)

    await Promise.all([visitor.close(), sup.context.close(), agent.context.close(), seed.admin.context.close()])
  })
})
