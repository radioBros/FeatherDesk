// SPDX-License-Identifier: AGPL-3.0-only

/**
 * FE-051: the public help center works signed out. An admin seeds a public
 * category + published article through a personal token, then an anonymous
 * visitor searches, opens it and votes once.
 */
import { test, expect, loginAs, tokenClient, ADMIN, watchConsole, a11y } from '../fixtures'

test.describe('public knowledge base', () => {
  test('anonymous search finds a published article and "helpful" is sent once', async ({ page, browser }, info) => {
    const suffix = `${info.project.name.replace(/[^a-z0-9]/gi, '').toLowerCase()}-${Date.now().toString(36)}`
    await loginAs(page, 'staff', ADMIN)
    const api = await tokenClient(page, ['kb:read', 'kb:write', 'admin'])
    const cat = await api.post('/admin/kb/categories', { slug: `e2e-${suffix}`, title: { en: `E2E help ${suffix}` }, visibility: 'public' })
    expect(cat.ok(), await cat.text()).toBeTruthy()
    const { id: categoryId } = await cat.json()
    const art = await api.post('/agent/kb/articles', {
      category_id: categoryId,
      slug: `reset-printer-${suffix}`,
      visibility: 'public',
      locale: 'en',
      title: `Reset the office printer ${suffix}`,
      body_md: 'Hold the **reset** button for five seconds.',
    })
    expect(art.ok(), await art.text()).toBeTruthy()
    const { id: articleId } = await art.json()
    const pub = await api.patch(`/agent/kb/articles/${articleId}`, { status: 'published' })
    expect(pub.ok(), await pub.text()).toBeTruthy()

    // A fresh, signed-out context.
    const ctx = await browser.newContext()
    const anon = await ctx.newPage()
    const problems = watchConsole(anon)
    await anon.goto('/kb')
    await expect(anon).toHaveURL(/\/kb$/)
    await anon.getByTestId('kb-search').locator('input').fill(`printer ${suffix}`)
    const item = anon.getByTestId('kb-article-item').filter({ hasText: suffix })
    await expect(item).toBeVisible()
    await item.click()
    await expect(anon.getByTestId('kb-article-title')).toContainText(suffix)
    await expect(anon.getByTestId('kb-article-body')).toContainText('reset')

    let votes = 0
    anon.on('request', (r) => {
      if (r.method() === 'POST' && r.url().includes(`/kb/articles/${articleId}/feedback`)) votes++
    })
    await anon.getByTestId('kb-helpful-yes').click()
    await expect(anon.getByTestId('kb-feedback-thanks')).toBeVisible()
    await anon.reload()
    await expect(anon.getByTestId('kb-article-title')).toBeVisible()
    expect(votes).toBe(1)
    expect(await a11y(anon)).toEqual([])
    expect(problems).toEqual([])
    await ctx.close()
  })
})
