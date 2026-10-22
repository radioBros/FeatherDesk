// SPDX-License-Identifier: AGPL-3.0-only

import CustomFieldsPage from './CustomFieldsPage.vue'
import CustomFieldForm from './CustomFieldForm.vue'
import { mountAgent, fx, json, byTestId, allByTestId, typeInto, click } from '../testing'
import type { components } from '@/api/schema'

type S = components['schemas']
const def = (extra: Partial<S['CustomFieldDef']> = {}): S['CustomFieldDef'] => ({
  id: 'cf-1',
  key: 'plan',
  label: { en: 'Plan', it: 'Piano' },
  type: 'select',
  options: [
    { value: 'gold', label: { en: 'Gold' } },
    { value: 'silver', label: { en: 'Silver' } },
  ],
  required: false,
  customer_visible: true,
  customer_editable: false,
  category_ids: null,
  sort: 1,
  active: true,
  ...extra,
})
type FormState = { $: { setupState: { model: { type: string | null; customer_visible: boolean; customer_editable: boolean } } } }
const state = (w: { vm: unknown }) => (w.vm as FormState).$.setupState.model

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('CustomFieldsPage (FE-053)', () => {
  it('lists active definitions; archived only on request', async () => {
    const h = await mountAgent(CustomFieldsPage, {
      me: fx.me('admin'),
      api: { 'GET /admin/custom-fields': [def(), def({ id: 'cf-2', key: 'old', active: false })], 'GET /agent/categories': [fx.category()] },
    })
    const rows = h.wrapper.findAll('[data-testid="cf-row"]')
    expect(rows).toHaveLength(1)
    expect(rows[0]!.text()).toContain('plan')
    expect(rows[0]!.text()).toContain('Plan')
    expect(rows[0]!.text()).toContain('All categories')
  })

  it('archives instead of deleting', async () => {
    const h = await mountAgent(CustomFieldsPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/custom-fields': [def()],
        'GET /agent/categories': [],
        'PATCH /admin/custom-fields/cf-1': (_u, init) => json({ ...def(), ...init.body }),
      },
    })
    await click(byTestId('cf-archive'))
    await h.flush()
    expect(h.calls('PATCH /admin/custom-fields/cf-1')[0]!.body).toEqual({ active: false })
    expect(h.calls(/^DELETE/)).toHaveLength(0)
  })
})

describe('CustomFieldForm (FE-053, FE-005)', () => {
  it('an empty submit sends nothing', async () => {
    const h = await mountAgent(CustomFieldForm, { props: { categories: [] } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })

  it('a select field with no options is rejected client-side', async () => {
    const h = await mountAgent(CustomFieldForm, { props: { categories: [] } })
    await typeInto(byTestId('cf-key'), 'plan')
    await typeInto(byTestId('cf-label-en'), 'Plan')
    state(h.wrapper).type = 'select'
    await h.flush()
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(byTestId('cf-options-check')!.textContent).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })

  it('duplicate option values are rejected client-side', async () => {
    const h = await mountAgent(CustomFieldForm, { props: { categories: [] } })
    await typeInto(byTestId('cf-key'), 'plan')
    await typeInto(byTestId('cf-label-en'), 'Plan')
    state(h.wrapper).type = 'select'
    await h.flush()
    await click(byTestId('cf-option-add'))
    await click(byTestId('cf-option-add'))
    await typeInto(byTestId('cf-option-value-0'), 'gold')
    await typeInto(byTestId('cf-option-value-1'), 'gold')
    await typeInto(byTestId('cf-option-label-0-en'), 'Gold')
    await typeInto(byTestId('cf-option-label-1-en'), 'Gold again')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This value is used more than once')
    expect(h.calls()).toHaveLength(0)
  })

  it('a key the API would refuse (dash, leading digit) is rejected client-side', async () => {
    const h = await mountAgent(CustomFieldForm, { props: { categories: [] } })
    for (const bad of ['order-id', '1plan']) {
      await typeInto(byTestId('cf-key'), bad)
      await typeInto(byTestId('cf-label-en'), 'Order ID')
      state(h.wrapper).type = 'text'
      await h.flush()
      await h.wrapper.find('form').trigger('submit')
      await h.flush()
      expect(byTestId('cf-key')!.textContent).toContain('underscores')
    }
    expect(h.calls()).toHaveLength(0)
  })

  it('customer_editable is off and disabled while customer_visible is off', async () => {
    const h = await mountAgent(CustomFieldForm, { props: { field: def({ customer_visible: true, customer_editable: true }), categories: [] } })
    expect(byTestId('cf-editable')!.querySelector('input')!.disabled).toBe(false)
    state(h.wrapper).customer_visible = false
    await h.flush()
    expect(state(h.wrapper).customer_editable).toBe(false)
    expect(byTestId('cf-editable')!.querySelector('input')!.disabled).toBe(true)
  })

  it('the key is read-only when editing and is not sent in the PATCH', async () => {
    const h = await mountAgent(CustomFieldForm, {
      props: { field: def(), categories: [] },
      api: { 'PATCH /admin/custom-fields/cf-1': (_u, init) => json({ ...def(), ...init.body }) },
    })
    expect(byTestId('cf-key')!.querySelector('input')!.readOnly).toBe(true)
    await click(byTestId('cf-save'))
    const body = h.calls('PATCH /admin/custom-fields/cf-1')[0]!.body as Record<string, unknown>
    expect(body).not.toHaveProperty('key')
    expect(body).not.toHaveProperty('type')
    expect(body).toMatchObject({ label: { en: 'Plan', it: 'Piano' }, customer_visible: true, customer_editable: false })
    expect(allByTestId('cf-option')).toHaveLength(2)
  })

  it('creates a text field with key, type and category scope', async () => {
    const h = await mountAgent(CustomFieldForm, {
      props: { categories: [fx.category()] },
      api: { 'POST /admin/custom-fields': (_u, init) => json({ ...def(), ...init.body, id: 'cf-9' }, 201) },
    })
    await typeInto(byTestId('cf-key'), 'order_id')
    await typeInto(byTestId('cf-label-en'), 'Order ID')
    state(h.wrapper).type = 'text'
    await h.flush()
    await click(byTestId('cf-save'))
    expect(h.calls('POST /admin/custom-fields')[0]!.body).toMatchObject({ key: 'order_id', type: 'text', label: { en: 'Order ID' }, category_ids: null })
  })
})
