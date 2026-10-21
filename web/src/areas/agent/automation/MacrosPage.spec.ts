// SPDX-License-Identifier: AGPL-3.0-only

import MacrosPage from './MacrosPage.vue'
import MacroForm from './MacroForm.vue'
import { mountAgent, fx, json, byTestId, typeInto, click, type AgentHarness } from '../testing'
import { settleConfirm } from '@/composables/useConfirm'
import type { components } from '@/api/schema'

type S = components['schemas']

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const macro = (extra: Partial<S['Macro']> = {}): S['Macro'] => ({
  id: 'm-1',
  name: 'Close as solved',
  shared: false,
  owner_id: 'u-me',
  reply_kind: 'public',
  reply_md: 'Glad it works, {{customer.name}}!',
  status: 'resolved',
  priority: null,
  add_tags: ['solved'],
  remove_tags: [],
  assignee: 'me',
  project_id: null,
  created_at: fx.iso,
  updated_at: fx.iso,
  ...extra,
})
const lookups = { 'GET /agent/staff': [fx.staffRef()], 'GET /agent/tags': [] }

describe('MacrosPage (FE-064)', () => {
  it('personal page: lists own and shared macros; only the own ones are editable', async () => {
    const h = await mountAgent(MacrosPage, {
      api: { ...lookups, 'GET /agent/macros': [macro({ id: 'm-2', name: 'Team triage', shared: true, owner_id: null }), macro()] },
    })
    await h.flush()
    const rows = h.wrapper.findAll('[data-testid="macro-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.text()).toContain('Close as solved')
    expect(rows[0]!.text()).toContain('Resolved')
    expect(rows[0]!.text()).toContain('Assign to me')
    expect(rows[0]!.find('[data-testid="macro-edit"]').exists()).toBe(true)
    expect(rows[1]!.text()).toContain('Shared')
    expect(rows[1]!.find('[data-testid="macro-edit"]').exists()).toBe(false)
  })

  it('shared page reads and deletes through /admin/macros', async () => {
    const shared = macro({ id: 'm-2', shared: true, owner_id: null })
    const h = await mountAgent(MacrosPage, {
      me: fx.me('supervisor'),
      props: { shared: true },
      api: { ...lookups, 'GET /admin/macros': [shared], 'DELETE /admin/macros/m-2': () => new Response(null, { status: 204 }) },
    })
    await click(byTestId('macro-delete'))
    settleConfirm(true)
    await h.flush()
    expect(h.calls('GET /agent/macros')).toHaveLength(0)
    expect(h.calls('DELETE /admin/macros/m-2')).toHaveLength(1)
  })
})

function component(h: AgentHarness, name: string, testid: string) {
  const c = h.wrapper.findAllComponents({ name }).find((x) => x.attributes('data-testid') === testid)
  if (!c) throw new Error(testid)
  return c
}

describe('MacroForm (FE-064, FE-005)', () => {
  it('an empty submit sends nothing', async () => {
    const h = await mountAgent(MacroForm, { props: { staff: [], tags: [] } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })

  it('creates a personal macro; unchanged fields are sent as null and tags normalised', async () => {
    const h = await mountAgent(MacroForm, {
      props: { staff: [fx.staffRef()], tags: ['billing'] },
      api: { 'POST /agent/macros': (_u, init) => json(macro({ ...(init.body as object) }), 201) },
    })
    await typeInto(byTestId('macro-name'), 'Ask for logs')
    await typeInto(byTestId('macro-reply'), 'Could you send the logs?')
    component(h, 'VSelect', 'macro-status').vm.$emit('update:modelValue', 'pending')
    component(h, 'VCombobox', 'macro-add-tags').vm.$emit('update:modelValue', ['Needs-Info', 'needs-info'])
    await h.flush()
    await click(byTestId('macro-save'))
    expect(h.calls('POST /agent/macros')[0]!.body).toEqual({
      name: 'Ask for logs',
      reply_kind: 'public',
      reply_md: 'Could you send the logs?',
      status: 'pending',
      priority: null,
      add_tags: ['needs-info'],
      remove_tags: [],
      assignee: null,
    })
  })

  it('an invalid tag blocks the save', async () => {
    const h = await mountAgent(MacroForm, { props: { staff: [], tags: [] } })
    await typeInto(byTestId('macro-name'), 'Bad tag')
    component(h, 'VCombobox', 'macro-add-tags').vm.$emit('update:modelValue', ['not a tag!'])
    await h.flush()
    await click(byTestId('macro-save'))
    expect(h.calls()).toHaveLength(0)
  })

  it('a shared macro is saved through /admin/macros with PATCH when editing', async () => {
    const h = await mountAgent(MacroForm, {
      props: { macro: macro({ shared: true, owner_id: null }), shared: true, staff: [], tags: [] },
      api: { 'PATCH /admin/macros/m-1': (_u, init) => json(macro({ ...(init.body as object), shared: true })) },
    })
    await click(byTestId('macro-save'))
    expect(h.calls('PATCH /admin/macros/m-1')[0]!.body).toMatchObject({ name: 'Close as solved', status: 'resolved', assignee: 'me', add_tags: ['solved'] })
  })
})
