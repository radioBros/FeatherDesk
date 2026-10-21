// SPDX-License-Identifier: AGPL-3.0-only

import KbArticlePage from './KbArticlePage.vue'
import KbArticleForm from './KbArticleForm.vue'
import { allowedVisibilities, canKb } from './kb'
import { mountAgent, fx, json, byTestId, typeInto, click } from '../testing'
import { settleConfirm } from '@/composables/useConfirm'
import type { KbArticleWithActions, KbCategory } from './kb'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const cats: KbCategory[] = [
  { id: 'kc-pub', slug: 'general', title: { en: 'General' }, visibility: 'public' },
  { id: 'kc-cust', slug: 'billing', title: { en: 'Billing' }, visibility: 'customers' },
  { id: 'kc-int', slug: 'runbooks', title: { en: 'Runbooks' }, visibility: 'internal' },
]
const article = (extra: Partial<KbArticleWithActions> = {}): KbArticleWithActions => ({
  id: 'a-1',
  slug: 'reset-password',
  title: 'Reset your password',
  body_md: 'Click **Forgot password**.',
  locale: 'en',
  category_id: 'kc-pub',
  status: 'draft',
  visibility: 'public',
  updated_at: fx.iso,
  translations: [{ article_id: 'a-1', locale: 'en', title: 'Reset your password', body_md: 'Click **Forgot password**.' }],
  allowed_actions: ['edit', 'delete'],
  ...extra,
})

/** Opens a Vuetify select by its data-testid and returns the option titles from the overlay. */
async function options(testid: string): Promise<string[]> {
  const field = byTestId(testid)?.querySelector('.v-field')
  field?.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
  await new Promise((r) => setTimeout(r, 0))
  await new Promise((r) => setTimeout(r, 0))
  return [...document.body.querySelectorAll('.v-overlay--active .v-list-item-title')].map((e) => e.textContent?.trim() ?? '')
}

function page(a: KbArticleWithActions, me = fx.me('agent')) {
  return mountAgent(KbArticlePage, {
    path: '/agent/kb/articles/:id',
    route: '/agent/kb/articles/a-1',
    me,
    config: { branding: { locales: ['en', 'it'] } as never },
    api: { 'GET /agent/kb/articles/a-1': a, 'GET /kb/categories': cats },
  })
}

describe('KbArticlePage (FE-052)', () => {
  it('has no Publish/Archive button when allowed_actions lacks them (agent)', async () => {
    const h = await page(article({ allowed_actions: [] }))
    expect(h.wrapper.text()).toContain('Reset your password')
    expect(byTestId('kb-publish')).toBeNull()
    expect(byTestId('kb-archive')).toBeNull()
  })

  it('shows Publish when the server allows it (supervisor) and PATCHes the status', async () => {
    let patched: unknown = null
    const h = await mountAgent(KbArticlePage, {
      path: '/agent/kb/articles/:id',
      route: '/agent/kb/articles/a-1',
      me: fx.me('supervisor'),
      config: { branding: { locales: ['en', 'it'] } as never },
      api: {
        'GET /agent/kb/articles/a-1': article({ allowed_actions: ['publish', 'archive'] }),
        'GET /kb/categories': cats,
        'PATCH /agent/kb/articles/a-1': (_u, init) => {
          patched = init.body
          return json(article({ status: 'published', allowed_actions: ['archive'] }))
        },
      },
    })
    expect(byTestId('kb-publish')).not.toBeNull()
    await click(byTestId('kb-publish'))
    await h.flush()
    expect(patched).toEqual({ status: 'published' })
  })

  it('reads only the KbAllowedAction values the server sent', () => {
    expect(canKb(article({ allowed_actions: ['publish'] }), 'publish')).toBe(true)
    expect(canKb(article({ allowed_actions: ['archive'] }), 'publish')).toBe(false)
    expect(canKb(article({ allowed_actions: [] }), 'edit')).toBe(false)
    expect(canKb(null, 'archive')).toBe(false)
  })

  it('a published article offers Unpublish (PATCH status draft) when allowed, never Publish', async () => {
    const h = await mountAgent(KbArticlePage, {
      path: '/agent/kb/articles/:id',
      route: '/agent/kb/articles/a-1',
      me: fx.me('supervisor'),
      config: { branding: { locales: ['en'] } as never },
      api: {
        'GET /agent/kb/articles/a-1': article({ status: 'published', allowed_actions: ['edit', 'unpublish', 'archive'] }),
        'GET /kb/categories': cats,
        'PATCH /agent/kb/articles/a-1': () => json(article({ allowed_actions: ['edit', 'publish', 'archive', 'delete'] })),
      },
    })
    expect(byTestId('kb-publish')).toBeNull()
    expect(byTestId('kb-delete')).toBeNull()
    await click(byTestId('kb-unpublish'))
    await h.flush()
    expect(h.calls('PATCH /agent/kb/articles/a-1')[0]!.body).toEqual({ status: 'draft' })
  })

  it('Delete follows `delete`, not the status: a draft without it has no Delete', async () => {
    await page(article({ allowed_actions: ['edit'] }))
    expect(byTestId('kb-delete')).toBeNull()
  })

  it('without `edit` the form is read-only and has no Save', async () => {
    await page(article({ status: 'published', allowed_actions: [] }))
    expect(byTestId('kb-readonly')).not.toBeNull()
    expect(byTestId('kb-save')).toBeNull()
    expect(byTestId('kb-title-en')?.querySelector('input')?.readOnly).toBe(true)
  })

  it('renders a tab per locale and badges the missing translation', async () => {
    await page(article())
    expect(byTestId('kb-tab-en')).not.toBeNull()
    expect(byTestId('kb-tab-it')?.querySelector('[data-testid="kb-missing"]')).not.toBeNull()
    expect(byTestId('kb-tab-en')?.querySelector('[data-testid="kb-missing"]')).toBeNull()
  })
})

describe('KbArticleForm (FE-052, FE-005)', () => {
  it('an empty title or category sends no request', async () => {
    const h = await mountAgent(KbArticleForm, { props: { categories: cats, locales: ['en', 'it'] } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })

  it('visibility options are never wider than the category (customers removes public)', async () => {
    expect(allowedVisibilities({ visibility: 'customers' })).toEqual(['customers', 'internal'])
    expect(allowedVisibilities({ visibility: 'internal' })).toEqual(['internal'])
    await mountAgent(KbArticleForm, {
      props: { article: article({ category_id: 'kc-cust', visibility: 'customers' }), categories: cats, locales: ['en'] },
    })
    const opts = await options('kb-visibility')
    expect(opts).toContain('Customers only')
    expect(opts).not.toContain('Public')
  })

  it('switching to a customers category narrows a public article', async () => {
    const h = await mountAgent(KbArticleForm, {
      props: { article: article(), translations: { en: { title: 'Reset your password', body_md: 'x' } }, categories: cats, locales: ['en'] },
      api: { 'PATCH /agent/kb/articles/a-1': (_u, init) => json({ ...article(), ...(init.body as object) }) },
    })
    const opts = await options('kb-category')
    expect(opts.some((o) => o.startsWith('Billing'))).toBe(true)
    const item = [...document.body.querySelectorAll('.v-overlay--active .v-list-item')].find((e) => e.textContent?.includes('Billing'))
    await click(item ?? null)
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.calls('PATCH /agent/kb/articles/a-1')[0]!.body).toEqual({ category_id: 'kc-cust', visibility: 'customers' })
  })

  it('saves only the changed translation with PUT and no metadata PATCH', async () => {
    const h = await mountAgent(KbArticleForm, {
      props: { article: article(), translations: { en: { title: 'Reset your password', body_md: 'x' } }, categories: cats, locales: ['en', 'it'] },
      api: { 'PUT /agent/kb/articles/a-1/translations/it': (_u, init) => json({ article_id: 'a-1', locale: 'it', ...(init.body as object) }) },
    })
    await typeInto(byTestId('kb-title-it'), 'Reimposta la password')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.calls('PUT /agent/kb/articles/a-1/translations/it')[0]!.body).toEqual({ title: 'Reimposta la password', body_md: '' })
    expect(h.calls(/^PATCH/)).toHaveLength(0)
    expect(h.calls('PUT /agent/kb/articles/a-1/translations/en')).toHaveLength(0)
  })

  it('removes a saved translation after confirm (DELETE); the default locale has no Remove', async () => {
    const h = await mountAgent(KbArticleForm, {
      props: {
        article: article(),
        translations: { en: { title: 'Reset your password', body_md: 'x' }, it: { title: 'Reimposta la password', body_md: 'y' } },
        categories: cats,
        locales: ['en', 'it'],
      },
      api: { 'DELETE /agent/kb/articles/a-1/translations/it': () => new Response(null, { status: 204 }) },
    })
    expect(byTestId('kb-remove-en')).toBeNull()
    await click(byTestId('kb-remove-it'))
    settleConfirm(true)
    await h.flush()
    expect(h.calls('DELETE /agent/kb/articles/a-1/translations/it')).toHaveLength(1)
    expect(h.wrapper.emitted('translationDeleted')).toEqual([['it']])
    expect(byTestId('kb-tab-it')?.querySelector('[data-testid="kb-missing"]')).not.toBeNull()
  })
})
