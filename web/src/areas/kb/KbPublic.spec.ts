// SPDX-License-Identifier: AGPL-3.0-only

import { createMemoryHistory, createRouter } from 'vue-router'
import KbHomePage from './pages/KbHomePage.vue'
import KbArticlePage from './pages/KbArticlePage.vue'
import KbCategoryPage from './pages/KbCategoryPage.vue'
import { resetKbState } from './kb'
import { decide } from '@/router/guards'
import { kbRoutes } from '@/router/routes'
import { setLocale } from '@/i18n'
import { mockFetch, json } from '@/test/mount'
import { mountPage, requests, resolveFor, settle } from '../portal/testing'

afterEach(async () => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
  resetKbState()
  await setLocale('en')
})

const CATS = [
  { id: 'k1', slug: 'printing', title: { en: 'Printing', it: 'Stampa' }, visibility: 'public', sort: 1, article_count: 2 },
  { id: 'k2', slug: 'toner', parent_id: 'k1', title: { en: 'Toner' }, visibility: 'public', sort: 1 },
]
const ARTICLE_EN = { id: 'a1', category_id: 'k1', slug: 'x', status: 'published', visibility: 'public', locale: 'en', title: 'Reset the printer', body_md: 'Hold **reset**.', updated_at: '2026-10-20T10:00:00Z' }

function server() {
  return mockFetch((url, init) => {
    const u = new URL(url, 'http://x')
    const path = u.pathname.replace('/api/v1', '')
    if ((init.method ?? 'GET') === 'POST') return new Response(null, { status: 204 })
    if (path === '/kb/categories') return json(CATS)
    if (path === '/kb/articles') return json({ items: u.searchParams.get('q') === 'nothing' ? [] : [{ ...ARTICLE_EN, excerpt: 'Hold reset' }], next_cursor: null })
    if (path.startsWith('/kb/articles/')) return json(ARTICLE_EN)
    return json({}, 404)
  })
}

describe('public KB (FE-051)', () => {
  it('anonymous navigation to /kb/a/x is allowed by the guard (no login redirect)', () => {
    const router = createRouter({ history: createMemoryHistory(), routes: kbRoutes })
    for (const p of ['/kb', '/kb/c/printing', '/kb/a/x']) expect(decide(resolveFor(router, p), { me: null, setupPending: false })).toBe(true)
  })

  it('renders an article anonymously', async () => {
    server()
    const { wrapper } = await mountPage(KbArticlePage, { path: '/kb/a/x', pattern: '/kb/a/:slug', me: null })
    await settle(8)
    expect(wrapper.find('[data-testid="kb-article-title"]').text()).toBe('Reset the printer')
    expect(wrapper.find('[data-testid="kb-article-body"]').html()).toContain('<strong>reset</strong>')
    expect(wrapper.find('[data-testid="kb-fallback"]').exists()).toBe(false)
  })

  it('an article missing in "it" renders the en text with the fallback notice', async () => {
    const { fn } = server()
    await setLocale('it')
    const { wrapper } = await mountPage(KbArticlePage, { path: '/kb/a/x', pattern: '/kb/a/:slug', me: null })
    await settle(8)
    const req = (await requests(fn)).find((r) => r.path === '/kb/articles/x')!
    expect(new URLSearchParams(req.search).get('locale')).toBe('it')
    expect(wrapper.find('[data-testid="kb-article-title"]').text()).toBe('Reset the printer')
    expect(wrapper.find('[data-testid="kb-fallback"]').text()).toContain('English')
  })

  it('"Was this helpful?" is sent once per article per tab', async () => {
    const { fn } = server()
    const first = await mountPage(KbArticlePage, { path: '/kb/a/x', pattern: '/kb/a/:slug', me: null })
    await settle(8)
    await first.wrapper.find('[data-testid="kb-helpful-yes"]').trigger('click')
    await settle(4)
    expect(first.wrapper.find('[data-testid="kb-feedback-thanks"]').exists()).toBe(true)
    first.wrapper.unmount()
    // same tab, article opened again: no second vote offered or sent
    const again = await mountPage(KbArticlePage, { path: '/kb/a/x', pattern: '/kb/a/:slug', me: null })
    await settle(8)
    expect(again.wrapper.find('[data-testid="kb-helpful-yes"]').exists()).toBe(false)
    const posts = (await requests(fn)).filter((r) => r.method === 'POST')
    expect(posts).toHaveLength(1)
    expect(posts[0]).toMatchObject({ path: '/kb/articles/a1/feedback', body: { helpful: true } })
  })

  it('home lists top-level categories and searches after 2 characters (debounced)', async () => {
    const { fn } = server()
    const { wrapper } = await mountPage(KbHomePage, { path: '/kb', pattern: '/kb', me: null })
    await settle(8)
    expect(wrapper.findAll('[data-testid="kb-category-card"]')).toHaveLength(1)
    expect(wrapper.text()).toContain('Printing')
    vi.useFakeTimers()
    await wrapper.find('[data-testid="kb-search"] input').setValue('p')
    await vi.advanceTimersByTimeAsync(400)
    expect((await requests(fn)).some((r) => r.path === '/kb/articles')).toBe(false)
    await wrapper.find('[data-testid="kb-search"] input').setValue('printer')
    await vi.advanceTimersByTimeAsync(400)
    vi.useRealTimers()
    await settle(6)
    const search = (await requests(fn)).filter((r) => r.path === '/kb/articles')
    expect(search).toHaveLength(1)
    expect(new URLSearchParams(search[0]!.search).get('q')).toBe('printer')
    expect(wrapper.findAll('[data-testid="kb-article-item"]')).toHaveLength(1)
  })

  it('a category page shows its sub-categories and articles', async () => {
    const { fn } = server()
    const { wrapper } = await mountPage(KbCategoryPage, { path: '/kb/c/printing', pattern: '/kb/c/:slug', me: null })
    await settle(8)
    expect(wrapper.find('[data-testid="kb-category-title"]').text()).toBe('Printing')
    expect(wrapper.text()).toContain('Toner')
    const list = (await requests(fn)).find((r) => r.path === '/kb/articles')!
    expect(new URLSearchParams(list.search).get('category')).toBe('printing')
    expect(wrapper.findAll('[data-testid="kb-article-item"]')).toHaveLength(1)
  })
})
