// SPDX-License-Identifier: AGPL-3.0-only

import KbCategoriesPage from './KbCategoriesPage.vue'
import KbCategoryForm from './KbCategoryForm.vue'
import { descendantsOf, flattenTree, type KbCategory } from './kb'
import { mountAgent, fx, json, byTestId, typeInto, click } from '../testing'
import { settleConfirm } from '@/composables/useConfirm'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const tree: KbCategory[] = [
  { id: 'b', slug: 'billing', title: { en: 'Billing' }, visibility: 'customers', sort: 20 },
  { id: 'a', slug: 'general', title: { en: 'General' }, visibility: 'public', sort: 10 },
  { id: 'a1', slug: 'accounts', title: { en: 'Accounts' }, visibility: 'public', sort: 10, parent_id: 'a' },
  { id: 'a1x', slug: 'sso', title: { en: 'SSO' }, visibility: 'internal', sort: 10, parent_id: 'a1' },
]

describe('KbCategoriesPage (FE-052)', () => {
  it('renders the tree: parents before children, siblings by sort', async () => {
    const h = await mountAgent(KbCategoriesPage, { me: fx.me('supervisor'), api: { 'GET /admin/kb/categories': tree } })
    const rows = h.wrapper.findAll('[data-testid="kbcat-row"]').map((r) => r.find('td').text())
    expect(rows).toEqual(['General', 'Accounts', 'SSO', 'Billing'])
  })

  it('moving a category down renumbers its siblings with full PATCH bodies', async () => {
    const h = await mountAgent(KbCategoriesPage, {
      me: fx.me('admin'),
      api: { 'GET /admin/kb/categories': tree, 'PATCH /admin/kb/categories/a': (_u, i) => json(i.body), 'PATCH /admin/kb/categories/b': (_u, i) => json(i.body) },
    })
    await click(h.wrapper.findAll('[data-testid="kbcat-down"]')[0]!.element)
    await h.flush()
    expect(h.calls('PATCH /admin/kb/categories/b')[0]!.body).toMatchObject({ sort: 10, slug: 'billing', visibility: 'customers' })
    expect(h.calls('PATCH /admin/kb/categories/a')[0]!.body).toMatchObject({ sort: 20, slug: 'general' })
  })

  it('deletes after confirm', async () => {
    const h = await mountAgent(KbCategoriesPage, {
      me: fx.me('admin'),
      api: { 'GET /admin/kb/categories': tree, 'DELETE /admin/kb/categories/b': new Response(null, { status: 204 }) },
    })
    const del = h.wrapper.findAll('[data-testid="kbcat-delete"]')
    await click(del[del.length - 1]!.element)
    settleConfirm(true)
    await h.flush()
    expect(h.calls('DELETE /admin/kb/categories/b')).toHaveLength(1)
  })

  it('creates a category from the dialog', async () => {
    let body: unknown
    const h = await mountAgent(KbCategoriesPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/kb/categories': [],
        'POST /admin/kb/categories': (_u, i) => {
          body = i.body
          return json({ id: 'n', ...(i.body as object) }, 201)
        },
      },
    })
    await click(byTestId('kbcat-new'))
    await typeInto(byTestId('kbcat-title-en'), 'Getting started')
    await typeInto(byTestId('kbcat-slug'), 'getting-started')
    // visibility via the select overlay
    byTestId('kbcat-visibility')?.querySelector('.v-field')?.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    await h.flush()
    const opt = [...document.body.querySelectorAll('.v-overlay--active .v-list-item')].find((e) => e.textContent?.includes('Public'))
    await click(opt ?? null)
    await click(byTestId('kbcat-save'))
    await h.flush()
    expect(body).toMatchObject({ slug: 'getting-started', title: { en: 'Getting started' }, visibility: 'public', parent_id: null })
  })
})

describe('KbCategoryForm', () => {
  it('an empty submit sends nothing; a bad slug is rejected client-side', async () => {
    const h = await mountAgent(KbCategoryForm, { props: { categories: tree } })
    await typeInto(byTestId('kbcat-slug'), 'Not A Slug')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.wrapper.text()).toContain('Use lowercase letters, numbers and dashes only')
    expect(h.calls()).toHaveLength(0)
  })

  it('the parent list excludes the category itself and its descendants', () => {
    expect([...descendantsOf(tree, 'a')].sort()).toEqual(['a', 'a1', 'a1x'])
    expect(flattenTree(tree, (c) => c.slug).map((r) => [r.item.id, r.depth])).toEqual([
      ['a', 0],
      ['a1', 1],
      ['a1x', 2],
      ['b', 0],
    ])
  })
})
