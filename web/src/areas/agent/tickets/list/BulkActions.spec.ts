// SPDX-License-Identifier: AGPL-3.0-only

import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
import BulkActionForm from './BulkActionForm.vue'
import SaveViewForm from './SaveViewForm.vue'
import { mountAgent, fx, json } from '../../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('BulkActionForm (FE-038)', () => {
  it('patches sequentially and shows per-row results on partial failure', async () => {
    let inFlight = false
    let overlapped = false
    const h = await mountAgent(BulkActionForm, {
      props: { tickets: [{ id: 't-1', number: 1 }, { id: 't-2', number: 2 }], staff: [], teams: [], meId: 'u-me' },
      api: {
        'PATCH /agent/tickets/t-1': async () => {
          inFlight = true
          await new Promise((r) => setTimeout(r, 20))
          inFlight = false
          return json({ ticket: fx.ticket(), visible: true })
        },
        'PATCH /agent/tickets/t-2': () => {
          overlapped = inFlight
          return json({ type: 'https://featherdesk.dev/problems/invalid-transition', title: 'Invalid transition', status: 409, detail: 'Closed tickets cannot be resolved', allowed: ['open'] }, 409)
        },
      },
    })
    const selects = h.wrapper.findAllComponents(AutocompleteSearch)
    await selects[0]!.setValue('status')
    await h.flush()
    await selects[1]!.setValue('resolved')
    await h.wrapper.find('form').trigger('submit')
    // Wait for both sequential PATCHes (a fixed sleep is flaky under load and lets the
    // second request leak into the next test).
    await vi.waitFor(() => expect(h.calls(/^PATCH/).map((c) => c.path)).toEqual(['/agent/tickets/t-1', '/agent/tickets/t-2']), { timeout: 3000 })
    await vi.waitFor(() => expect(h.wrapper.emitted('done')).toBeTruthy(), { timeout: 3000 })
    await h.flush()
    expect(h.calls('PATCH /agent/tickets/t-1')[0]!.body).toEqual({ status: 'resolved' })
    expect(overlapped).toBe(false)
    expect(h.wrapper.findAll('[data-testid="bulk-ok"]')).toHaveLength(1)
    const err = h.wrapper.find('[data-testid="bulk-error"]')
    expect(err.text()).toContain('#2')
    expect(err.text()).toContain('Closed tickets cannot be resolved')
    expect(h.wrapper.emitted('done')![0]![0]).toHaveLength(2)
  })

  it('assign to nobody sends assignee_id null; an empty submit sends nothing', async () => {
    const h = await mountAgent(BulkActionForm, {
      props: { tickets: [{ id: 't-1', number: 1 }], staff: [], teams: [], meId: 'u-me' },
      api: { 'PATCH /agent/tickets/t-1': () => json({ ticket: fx.ticket(), visible: true }) },
    })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.calls()).toHaveLength(0)
    expect(h.wrapper.text()).toContain('Choose an option')
    const selects = h.wrapper.findAllComponents(AutocompleteSearch)
    await selects[0]!.setValue('assign')
    await h.flush()
    await selects[1]!.setValue('none')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.calls('PATCH /agent/tickets/t-1')[0]!.body).toEqual({ assignee_id: null })
  })
})

describe('SaveViewForm (FE-038)', () => {
  it('an empty submit sends nothing', async () => {
    const h = await mountAgent(SaveViewForm, { props: { filter: { status: 'open' } } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.calls()).toHaveLength(0)
    expect(h.wrapper.text()).toContain('This field is required')
  })
  it('posts the name, shared flag and the current filter', async () => {
    const h = await mountAgent(SaveViewForm, {
      props: { filter: { status: 'open', 'cf.plan': 'gold' } },
      api: { 'POST /agent/views': (_u, init) => json({ id: 'v-1', ...init.body }, 201) },
    })
    await h.wrapper.find('[data-testid="view-name"] input').setValue('Gold open')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.calls('POST /agent/views')[0]!.body).toEqual({ name: 'Gold open', shared: false, filter: { status: 'open', 'cf.plan': 'gold' } })
    expect(h.wrapper.emitted('saved')).toBeTruthy()
  })
})
