// SPDX-License-Identifier: AGPL-3.0-only

import KbListPage from './KbListPage.vue'
import { mountAgent, fx, byTestId } from '../testing'
import { scrollTableToEnd } from '@/test/mount'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const summary = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  slug: `art-${id}`,
  title: `Article ${id}`,
  locale: 'en',
  status: 'draft',
  visibility: 'public',
  category_id: 'kc-1',
  updated_at: fx.iso,
  available_locales: ['en'],
  ...extra,
})

describe('KbListPage (FE-052)', () => {
  it('lists articles with status, category and locale coverage', async () => {
    const h = await mountAgent(KbListPage, {
      config: { branding: { locales: ['en', 'it'] } as never },
      api: {
        'GET /agent/kb/articles': fx.page([summary('1', { status: 'published', available_locales: ['en', 'it'] }), summary('2')], 'c2'),
        'GET /kb/categories': [{ id: 'kc-1', slug: 'general', title: { en: 'General' }, visibility: 'public' }],
      },
    })
    await h.flush()
    const rows = h.wrapper.findAll('[data-testid="kb-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.text()).toContain('Published')
    expect(rows[0]!.text()).toContain('General')
    expect(rows[1]!.text()).toContain('Draft')
    expect(byTestId('kb-new')?.getAttribute('href')).toBe('/agent/kb/articles/new')
  })

  it('loads the next page with the cursor', async () => {
    const h = await mountAgent(KbListPage, {
      api: {
        'GET /agent/kb/articles': (url: URL) =>
          new Response(JSON.stringify(url.searchParams.get('cursor') ? fx.page([summary('3')]) : fx.page([summary('1')], 'c2')), {
            headers: { 'Content-Type': 'application/json' },
          }),
        'GET /kb/categories': [],
      },
    })
    scrollTableToEnd(h.wrapper.element)
    await h.flush()
    expect(h.calls(/GET \/agent\/kb\/articles\?.*cursor=c2/)).toHaveLength(1)
    expect(h.wrapper.findAll('[data-testid="kb-row"]')).toHaveLength(2)
  })

  it('shows the empty state', async () => {
    const h = await mountAgent(KbListPage, { api: { 'GET /agent/kb/articles': fx.page([]), 'GET /kb/categories': [] } })
    expect(h.wrapper.find('[data-testid="empty-state"]').exists()).toBe(true)
  })
})
