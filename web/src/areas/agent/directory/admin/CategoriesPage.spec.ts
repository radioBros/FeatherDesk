// SPDX-License-Identifier: AGPL-3.0-only

import CategoriesPage from './CategoriesPage.vue'
import CategoryForm from './CategoryForm.vue'
import { mountAgent, fx, json, byTestId, typeInto, click } from '../../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('CategoriesPage (FE-028)', () => {
  it('renders the categories with their default team', async () => {
    const h = await mountAgent(CategoriesPage, {
      me: fx.me('supervisor'),
      api: {
        'GET /admin/categories': [fx.category(), fx.category({ id: 'cat-2', name: 'Hardware', sort: 2, default_team_id: 'team-1' })],
        'GET /agent/teams': [fx.team()],
      },
    })
    await h.flush()
    const rows = h.wrapper.findAll('[data-testid="category-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[1]!.text()).toContain('Support')
  })

  it('creates a category and reloads the list', async () => {
    let created: unknown = null
    const h = await mountAgent(CategoriesPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/categories': [],
        'GET /agent/teams': [fx.team()],
        'POST /admin/categories': (_u, init) => {
          created = init.body
          return json(fx.category({ name: 'Billing' }), 201)
        },
      },
    })
    await click(byTestId('category-new'))
    await typeInto(byTestId('category-name'), 'Billing')
    await click(byTestId('category-save'))
    await h.flush()
    expect(created).toMatchObject({ name: 'Billing', active: true })
    expect(h.calls('GET /admin/categories')).toHaveLength(2)
  })
})

describe('CategoryForm (FE-005)', () => {
  it('an empty submit shows the required message and sends nothing', async () => {
    const h = await mountAgent(CategoryForm, { props: { teams: [] } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })
  it('edits send a PATCH to the category', async () => {
    const h = await mountAgent(CategoryForm, {
      props: { category: fx.category(), teams: [] },
      api: { 'PATCH /admin/categories/cat-1': (_u: URL, init: { body?: unknown }) => json({ ...fx.category(), ...(init.body as object) }) },
    })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.calls('PATCH /admin/categories/cat-1')[0]!.body).toMatchObject({ name: 'Billing' })
    expect(h.wrapper.emitted('saved')).toBeTruthy()
  })
})
