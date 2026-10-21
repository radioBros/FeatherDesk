// SPDX-License-Identifier: AGPL-3.0-only

import { defineComponent, h, ref } from 'vue'
import NewTicketForm from './forms/NewTicketForm.vue'
import CustomFieldInput from '@/components/CustomFieldInput.vue'
import TicketDetailPage from './pages/TicketDetailPage.vue'
import { mockFetch, json, mountApp, flush } from '@/test/mount'
import { requests, settle, mountPage } from './testing'
import type { CustomFieldDef, CustomFieldValue } from './types'

vi.mock('@/composables/useRealtime', () => ({ useRealtime: () => ({ connected: ref(true) }) }))
afterEach(() => vi.unstubAllGlobals())

const def = (key: string, extra: Partial<CustomFieldDef> = {}): CustomFieldDef => ({
  id: key,
  key,
  label: { en: key.toUpperCase() + ' label' },
  type: 'text',
  required: false,
  customer_visible: true,
  customer_editable: true,
  active: true,
  sort: 1,
  category_ids: null,
  ...extra,
})
const FIELDS = [
  def('order_id', { required: true }),
  def('internal_score', { customer_visible: false, customer_editable: false, type: 'number' }),
  def('plan', { customer_editable: false }),
  def('serial', { category_ids: ['c2'] }),
]
const CATEGORIES = [
  { id: 'c1', name: 'Billing', active: true, sort: 1 },
  { id: 'c2', name: 'Hardware', active: true, sort: 2 },
]

describe('custom fields in the portal (FE-054)', () => {
  it('a staff-only field is never rendered; non-editable ones are not inputs on the new form', async () => {
    mockFetch()
    const { wrapper } = mountApp(NewTicketForm, { props: { fields: FIELDS, categories: CATEGORIES } })
    await flush()
    expect(wrapper.text()).not.toContain('INTERNAL_SCORE label')
    expect(wrapper.find('[data-testid="cf-internal_score"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="cf-plan"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="cf-order_id"]').exists()).toBe(true)
  })

  it('changing the category swaps the rendered field set', async () => {
    mockFetch()
    const { wrapper } = mountApp(NewTicketForm, { props: { fields: FIELDS, categories: CATEGORIES } })
    await flush()
    expect(wrapper.find('[data-testid="cf-serial"]').exists()).toBe(false)
    ;(wrapper.vm as unknown as { $: { setupState: { model: { category_id: string } } } }).$.setupState.model.category_id = 'c2'
    await flush()
    expect(wrapper.find('[data-testid="cf-serial"]').exists()).toBe(true)
  })

  it('a required customer-editable field left empty blocks the request; filled values are sent', async () => {
    const { fn } = mockFetch(() => json({ id: 'n', number: 1 }, 201))
    const { wrapper } = mountApp(NewTicketForm, { props: { fields: FIELDS, categories: CATEGORIES } })
    await flush()
    await wrapper.find('[data-testid="nt-subject"] input').setValue('S')
    await wrapper.find('[data-testid="md-input"] textarea').setValue('B')
    await wrapper.find('form').trigger('submit')
    await settle()
    expect(fn).not.toHaveBeenCalled()
    await wrapper.find('[data-testid="cf-order_id"] input').setValue('A-42')
    await wrapper.find('form').trigger('submit')
    await settle(8)
    const [post] = await requests(fn)
    expect(post!.body).toMatchObject({ custom_fields: { order_id: 'A-42' } })
    expect(Object.keys((post!.body as { custom_fields: object }).custom_fields)).toEqual(['order_id'])
  })

  it('a number field rejects "abc" through the generated rule', async () => {
    const Host = defineComponent({
      setup() {
        const v = ref<CustomFieldValue>('')
        return () => h('div', [h(CustomFieldInput, { def: def('qty', { type: 'number' }), modelValue: v.value, 'onUpdate:modelValue': (x: CustomFieldValue) => (v.value = x) })])
      },
    })
    const { wrapper } = mountApp(Host)
    await flush()
    const input = wrapper.find('[data-testid="cf-qty"] input')
    // jsdom keeps "abc" in a number input as an empty value; set the model through the element instead.
    ;(input.element as HTMLInputElement).type = 'text'
    await input.setValue('abc')
    await input.trigger('blur')
    await settle()
    expect(wrapper.text()).toContain('Enter a number')
  })

  it('the ticket detail shows customer-visible values only', async () => {
    mockFetch((url) => {
      if (url.includes('/portal/custom-fields')) return json(FIELDS)
      return json({
        id: 't1', number: 1, subject: 'S', status: 'open', priority: 'normal', requester: { id: 'u', name: 'C', kind: 'customer' },
        messages: [], allowed_actions: [], created_at: '2026-10-20T10:00:00Z', updated_at: '2026-10-20T10:00:00Z',
        custom_fields: { order_id: 'A-42', plan: 'gold', internal_score: 9 },
      })
    })
    const { wrapper } = await mountPage(TicketDetailPage, { path: '/tickets/t1', pattern: '/tickets/:id' })
    await settle(8)
    expect(wrapper.find('[data-testid="cf-value-order_id"]').text()).toContain('A-42')
    expect(wrapper.find('[data-testid="cf-value-plan"]').text()).toContain('gold')
    expect(wrapper.text()).not.toContain('INTERNAL_SCORE label')
  })
})
