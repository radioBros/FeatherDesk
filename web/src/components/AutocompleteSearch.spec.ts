// SPDX-License-Identifier: AGPL-3.0-only

import { defineComponent, ref } from 'vue'
import AutocompleteSearch from './AutocompleteSearch.vue'
import { mountApp, flush } from '@/test/mount'
import { validations } from '@/constants/validations'
import { i18n } from '@/i18n'

afterEach(() => {
  vi.useRealTimers()
  document.body.innerHTML = ''
})

const ITEMS = [
  { title: 'Alpha', value: 'a', email: 'first@x.test' },
  { title: 'Beta', value: 'b', email: 'second@x.test' },
  { title: 'Gamma', value: 'g', email: 'third@x.test' },
  { title: 'Delta', value: 'd', email: 'fourth@x.test' },
]

type Props = Record<string, unknown>
function mountSel(props: Props = {}, initial: unknown = null) {
  const Host = defineComponent({
    components: { AutocompleteSearch },
    setup() {
      const v = ref<unknown>(initial)
      return { v, props }
    },
    template: `<AutocompleteSearch v-model="v" v-bind="props" label="Pick" data-testid="sel" ref="s" />`,
  })
  const r = mountApp(Host)
  return { ...r, comp: () => r.wrapper.findComponent(AutocompleteSearch), model: () => (r.wrapper.vm as unknown as { v: unknown }).v }
}
async function open(w: ReturnType<typeof mountSel>) {
  await w.wrapper.find('.v-field').trigger('mousedown')
  await flush()
  await flush()
}
const search = () => document.body.querySelector<HTMLInputElement>('[data-testid="autocomplete-search-input"] input')
const optionTitles = () => [...document.body.querySelectorAll('.v-overlay-container .v-list-item-title')].map((e) => e.textContent?.trim())
async function type(text: string) {
  const el = search()!
  el.value = text
  el.dispatchEvent(new Event('input', { bubbles: true }))
  await flush()
}

describe('AutocompleteSearch', () => {
  it('filters by title client-side', async () => {
    const w = mountSel({ items: ITEMS })
    await open(w)
    expect(optionTitles()).toEqual(['Alpha', 'Beta', 'Gamma', 'Delta'])
    await type('ta')
    expect(optionTitles()).toEqual(['Beta', 'Delta'])
  })

  it('filters over searchFields too', async () => {
    const w = mountSel({ items: ITEMS, searchFields: ['email'] })
    await open(w)
    await type('third@')
    expect(optionTitles()).toEqual(['Gamma'])
  })

  it('shows the translated "No results" and the translated search placeholder', async () => {
    const w = mountSel({ items: ITEMS })
    await open(w)
    expect(search()!.placeholder).toBe(i18n.global.t('common.search'))
    await type('zzzz')
    expect(document.body.textContent).toContain(i18n.global.t('common.noResults'))
  })

  it('hideSearch drops the search box', async () => {
    const w = mountSel({ items: ITEMS, hideSearch: true })
    await open(w)
    expect(search()).toBeNull()
    expect(optionTitles()).toHaveLength(4)
  })

  it('passes rules through to the field', async () => {
    const w = mountSel({ items: ITEMS, rules: [validations.required.select] })
    const sel = w.wrapper.findComponent({ name: 'VSelect' })
    expect(sel.props('rules')).toHaveLength(1)
    await (sel.vm as unknown as { validate: () => Promise<unknown> }).validate()
    await flush()
    expect(w.wrapper.find('.v-messages__message').exists()).toBe(true)
  })

  it('supports plain string items and the v-model', async () => {
    const w = mountSel({ items: ['red', 'green'] })
    await open(w)
    expect(optionTitles()).toEqual(['red', 'green'])
    await type('gre')
    expect(optionTitles()).toEqual(['green'])
  })

  it('exposes reset() that clears the model and the search text', async () => {
    const w = mountSel({ items: ITEMS, clearable: true }, 'a')
    await open(w)
    await type('be')
    ;(w.comp().vm as unknown as { reset: () => void }).reset()
    await flush()
    expect(search()!.value).toBe('')
  })

  describe('compactMultiple', () => {
    const props = { items: ITEMS, multiple: true, compactMultiple: true }
    it('shows chips up to the limit', async () => {
      const w = mountSel(props, ['a', 'b'])
      await flush()
      expect(w.wrapper.findAll('.autocomplete-search__chip')).toHaveLength(2)
      expect(w.wrapper.find('.autocomplete-search__summary').exists()).toBe(false)
    })
    it('collapses to one "N selected" summary above the limit', async () => {
      const w = mountSel(props, ['a', 'b', 'g'])
      await flush()
      expect(w.wrapper.findAll('.autocomplete-search__chip')).toHaveLength(0)
      expect(w.wrapper.find('.autocomplete-search__summary').text()).toBe(i18n.global.t('common.nSelected', { n: 3 }))
    })
    it('lists the selected options first when the menu opens', async () => {
      const w = mountSel(props, ['g', 'd'])
      await open(w)
      expect(optionTitles()).toEqual(['Gamma', 'Delta', 'Alpha', 'Beta'])
    })
  })

  describe('async mode (fetchItems)', () => {
    const rows = (q: string) => [{ title: `R-${q}`, value: `r-${q}` }]
    it('debounces the search box, shows loading, and replaces the items', async () => {
      const fetchItems = vi.fn(async (q: string) => rows(q))
      const w = mountSel({ fetchItems })
      await open(w)
      expect(fetchItems).toHaveBeenCalledWith('') // no initial items: the open calls fetchItems('')
      await flush()
      vi.useFakeTimers()
      const el = search()!
      for (const t of ['a', 'ab', 'abc']) {
        el.value = t
        el.dispatchEvent(new Event('input', { bubbles: true }))
        await vi.advanceTimersByTimeAsync(100)
      }
      expect(fetchItems).toHaveBeenCalledTimes(1) // still debouncing
      expect(w.comp().findComponent({ name: 'VSelect' }).props('loading')).toBe(true)
      await vi.advanceTimersByTimeAsync(300)
      vi.useRealTimers()
      expect(fetchItems).toHaveBeenCalledTimes(2)
      expect(fetchItems).toHaveBeenLastCalledWith('abc')
      await flush()
      expect(optionTitles()).toEqual(['R-abc'])
    })

    it('keeps the initial items on open and skips the client-side filter', async () => {
      const fetchItems = vi.fn(async () => [{ title: 'Remote only', value: 'x' }])
      const w = mountSel({ fetchItems, items: ITEMS })
      await open(w)
      expect(fetchItems).not.toHaveBeenCalled()
      expect(optionTitles()).toHaveLength(4)
    })

    it('ignores a stale response that lands after a newer one', async () => {
      const resolvers: Record<string, (v: unknown[]) => void> = {}
      const fetchItems = vi.fn((q: string) => new Promise<unknown[]>((res) => (resolvers[q] = res)))
      const w = mountSel({ fetchItems, items: ITEMS, debounceMs: 0 })
      await open(w)
      await type('one')
      await new Promise((r) => setTimeout(r, 5))
      await type('two')
      await new Promise((r) => setTimeout(r, 5))
      resolvers['two']!([{ title: 'Two', value: 't' }])
      await flush()
      resolvers['one']!([{ title: 'One', value: 'o' }])
      await flush()
      expect(optionTitles()).toEqual(['Two'])
    })

    it('still renders the selected title when it is not in the results', async () => {
      const fetchItems = vi.fn(async () => [{ title: 'Other', value: 'o' }])
      const w = mountSel({ fetchItems, initialItems: [{ title: 'Chosen', value: 'c' }] }, 'c')
      await flush()
      expect(w.wrapper.find('.v-select__selection').text()).toBe('Chosen')
      await open(w)
      await flush()
      expect(optionTitles()).toEqual(['Other'])
      expect(w.wrapper.find('.v-select__selection').text()).toBe('Chosen')
    })
  })
})
