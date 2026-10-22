// SPDX-License-Identifier: AGPL-3.0-only

import CannedPage from './CannedPage.vue'
import CannedForm from './CannedForm.vue'
import { mountAgent, fx, json, byTestId, typeInto, click } from '../testing'
import { settleConfirm } from '@/composables/useConfirm'

const canned = { id: 'cr-1', title: 'Greeting', body_md: 'Hi {{requester.name}}', team_id: 'team-1', created_at: fx.iso, updated_at: fx.iso }

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('CannedPage (FE-041)', () => {
  it('lists canned responses with their team scope', async () => {
    const h = await mountAgent(CannedPage, {
      me: fx.me('supervisor'),
      api: { 'GET /admin/canned-responses': [canned, { ...canned, id: 'cr-2', title: 'Bye', team_id: null }], 'GET /agent/teams': [fx.team()] },
    })
    await h.flush()
    const rows = h.wrapper.findAll('[data-testid="canned-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.text()).toContain('All teams')
    expect(rows[1]!.text()).toContain('Support')
  })

  it('deletes after confirmation', async () => {
    const h = await mountAgent(CannedPage, {
      me: fx.me('supervisor'),
      api: { 'GET /admin/canned-responses': [canned], 'GET /agent/teams': [], 'DELETE /admin/canned-responses/cr-1': () => new Response(null, { status: 204 }) },
    })
    await click(byTestId('canned-delete'))
    settleConfirm(true)
    await h.flush()
    expect(h.calls('DELETE /admin/canned-responses/cr-1')).toHaveLength(1)
  })
})

describe('CannedForm (FE-041, FE-005)', () => {
  it('an empty submit sends nothing', async () => {
    const h = await mountAgent(CannedForm, { props: { teams: [] } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })
  it('creates a global canned response and lists the variables', async () => {
    const h = await mountAgent(CannedForm, {
      props: { teams: [fx.team()] },
      api: { 'POST /admin/canned-responses': (_u, init) => json({ id: 'cr-9', ...init.body }, 201) },
    })
    expect(byTestId('canned-variables')!.textContent).toContain('{{requester.name}}')
    await typeInto(byTestId('canned-title'), 'Welcome')
    await typeInto(byTestId('md-input'), 'Hello {{requester.name}}')
    await click(byTestId('canned-save'))
    expect(h.calls('POST /admin/canned-responses')[0]!.body).toEqual({ title: 'Welcome', body_md: 'Hello {{requester.name}}', team_id: null })
  })
})
