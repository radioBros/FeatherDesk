// SPDX-License-Identifier: AGPL-3.0-only

import TagsPage from './TagsPage.vue'
import { mountAgent, fx, typeInto, byTestId } from '../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('TagsPage (FE-041)', () => {
  it('lists tags by usage, read-only', async () => {
    const h = await mountAgent(TagsPage, {
      me: fx.me('supervisor'),
      api: {
        'GET /agent/tags': [
          { id: 'g1', name: 'vip', usage_count: 3 },
          { id: 'g2', name: 'billing', usage_count: 12 },
        ],
      },
    })
    const rows = h.wrapper.findAll('[data-testid="tag-row"]')
    expect(rows.map((r) => r.text())).toEqual(['billing12', 'vip3'])
    expect(h.wrapper.find('button[data-testid="tag-delete"]').exists()).toBe(false)
  })

  it('searches on the server with a debounce', async () => {
    const h = await mountAgent(TagsPage, { me: fx.me('supervisor'), api: { 'GET /agent/tags': [] } })
    await typeInto(byTestId('tags-search'), 'bi')
    await typeInto(byTestId('tags-search'), 'bil')
    expect(h.calls('GET /agent/tags')).toHaveLength(1)
    await new Promise((r) => setTimeout(r, 350))
    await h.flush()
    const calls = h.calls('GET /agent/tags')
    expect(calls).toHaveLength(2)
    expect(calls[1]!.url.searchParams.get('q')).toBe('bil')
  })
})
