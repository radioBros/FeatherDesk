// SPDX-License-Identifier: AGPL-3.0-only

import SlaPage from './SlaPage.vue'
import SlaForm from './SlaForm.vue'
import { mountAgent, fx, json, byTestId, typeInto, click } from '../testing'
import type { components } from '@/api/schema'

type S = components['schemas']
const policies: S['SlaPolicy'][] = (['low', 'normal', 'high', 'urgent'] as const).map((p, i) => ({
  priority: p,
  first_response_minutes: 480 / (i + 1),
  resolution_minutes: 2880 / (i + 1),
  calendar_mode: '24x7',
}))

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('SlaPage (FE-041, FE-056)', () => {
  it('renders a row per priority from the saved policies', async () => {
    const h = await mountAgent(SlaPage, { me: fx.me('supervisor'), api: { 'GET /admin/sla-policies': policies } })
    expect(h.wrapper.findAll('[data-testid^="sla-row-"]')).toHaveLength(4)
    expect((byTestId('sla-first-urgent')!.querySelector('input') as HTMLInputElement).value).toBe('120')
  })
})

describe('SlaForm (FE-041)', () => {
  it('an empty grid shows required errors and sends nothing', async () => {
    const h = await mountAgent(SlaForm, { props: { policies: [] } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })

  it.each([
    ['0', 'Enter a value between 1 and 525600'],
    ['1.5', 'Enter a whole number'],
  ])('rejects %s before sending', async (value, message) => {
    const h = await mountAgent(SlaForm, { props: { policies } })
    await typeInto(byTestId('sla-first-high'), value)
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain(message)
    expect(h.calls()).toHaveLength(0)
  })

  it('PUTs every policy with numbers and the calendar mode', async () => {
    const h = await mountAgent(SlaForm, {
      props: { policies: policies.map((p) => (p.priority === 'urgent' ? { ...p, calendar_mode: 'team' as const } : p)) },
      api: { 'PUT /admin/sla-policies': (_u, init) => json(init.body.policies) },
    })
    await typeInto(byTestId('sla-resolution-low'), '3000')
    await click(byTestId('sla-save'))
    const body = h.calls('PUT /admin/sla-policies')[0]!.body as { policies: S['SlaPolicy'][] }
    expect(body.policies).toHaveLength(4)
    expect(body.policies.find((p) => p.priority === 'low')).toEqual({ priority: 'low', first_response_minutes: 480, resolution_minutes: 3000, calendar_mode: '24x7' })
    expect(body.policies.find((p) => p.priority === 'urgent')!.calendar_mode).toBe('team')
    expect(h.wrapper.emitted('saved')).toBeTruthy()
  })
})
