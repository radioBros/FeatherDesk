// SPDX-License-Identifier: AGPL-3.0-only

import AuditLogPage from './AuditLogPage.vue'
import AuditFilterForm from './AuditFilterForm.vue'
import { mountAgent, fx, json, byTestId, typeInto, click } from '../testing'
import { scrollTableToEnd } from '@/test/mount'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const ENTRY = {
  id: 'a-1',
  action: 'staff.updated',
  actor: { id: 'u-1', name: 'Root Admin', kind: 'staff' },
  target_type: 'user',
  target_id: 'u-2',
  created_at: fx.iso,
  ip: '10.0.0.1',
  data: { role: { from: 'agent', to: 'supervisor' } },
}

describe('AuditLogPage (FE-042)', () => {
  it('renders entries and pages with the cursor', async () => {
    const h = await mountAgent(AuditLogPage, {
      me: fx.me('admin'),
      api: { 'GET /admin/audit-log': (u) => json(fx.page([{ ...ENTRY, id: u.searchParams.get('cursor') ?? 'a-1' }], u.searchParams.get('cursor') ? null : 'c2')) },
    })
    await h.flush()
    expect(h.wrapper.findAll('[data-testid="audit-row"]')).toHaveLength(1)
    expect(h.wrapper.text()).toContain('Root Admin')
    scrollTableToEnd(h.wrapper.element)
    await h.flush()
    expect(h.calls(/cursor=c2/)).toHaveLength(1)
    expect(h.wrapper.findAll('[data-testid="audit-row"]')).toHaveLength(2)
  })

  it('sends the filter params to the API', async () => {
    const h = await mountAgent(AuditLogPage, { me: fx.me('admin'), api: { 'GET /admin/audit-log': fx.page([]) } })
    await h.flush()
    await typeInto(byTestId('audit-action'), 'staff.updated')
    await typeInto(byTestId('audit-since'), '2026-10-01')
    await click(byTestId('audit-apply'))
    await h.flush()
    const last = h.calls('GET /admin/audit-log').at(-1)!
    expect(last.url.searchParams.get('action')).toBe('staff.updated')
    expect(last.url.searchParams.get('since')).toBe('2026-10-01T00:00:00Z')
    expect(h.wrapper.text()).toContain('No audit entries match')
  })
})

describe('AuditFilterForm (FE-005)', () => {
  it('applies the filters locally and never requests anything itself', async () => {
    const h = await mountAgent(AuditFilterForm, {})
    await typeInto(byTestId('audit-action'), ' auth.login ')
    await typeInto(byTestId('audit-since'), '2026-10-01')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.emitted('apply')![0]).toEqual([{ action: 'auth.login', actor_id: null, since: '2026-10-01' }])
    expect(h.calls()).toHaveLength(0)
  })
})
