// SPDX-License-Identifier: AGPL-3.0-only

import PrivacyPage from './PrivacyPage.vue'
import EraseForm from './EraseForm.vue'
import PrivacySettingsForm from './PrivacySettingsForm.vue'
import { mountAgent, fx, json, byTestId, allByTestId, typeInto, click, type Handler } from '../testing'
import { useUiStore } from '@/stores/ui'
import type { components } from '@/api/schema'

type S = components['schemas']
type Model = Record<string, unknown>
const model = (w: { vm: unknown }) => (w.vm as { $: { setupState: { model: Model } } }).$.setupState.model

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const ISO = '2026-10-20T10:00:00Z'
function req(extra: Partial<S['PrivacyRequest']> = {}): S['PrivacyRequest'] {
  return {
    id: 'r-1',
    type: 'erasure',
    status: 'pending',
    user_id: 'u-c',
    user: { id: 'u-c', name: 'Carla Customer', email: 'carla@example.com', kind: 'customer', status: 'active' },
    requested_by: 'u-c',
    include_internal: false,
    mode: null,
    file_size: 0,
    reason: 'Please forget me',
    error: '',
    decided_by: null,
    decided_at: null,
    completed_at: null,
    expires_at: null,
    download_url: null,
    created_at: ISO,
    ...extra,
  }
}
const SETTINGS: S['PrivacySettings'] = {
  retention: { closed_tickets_months: 0, closed_tickets_action: 'anonymize', inbound_email_days: 90, audit_log_months: 0, export_days: 7 },
  erasure_auto_approve_days: 0,
  erasure_auto_approve_mode: 'anonymize',
  policy_version: '',
}
const RESULT: S['ErasureResult'] = {
  user_id: 'u-c',
  mode: 'anonymize_and_redact',
  name: 'Deleted user #1',
  tickets_kept: 2,
  messages_redacted: 3,
  attachments_deleted: 1,
  field_values_redacted: 0,
  request: req({ status: 'completed' }),
}
const ready = req({ id: 'r-2', type: 'export', status: 'ready', reason: '', file_size: 2048, download_url: '/api/v1/admin/privacy/exports/r-2', expires_at: ISO })

async function mountPage(api: Record<string, Handler | object> = {}) {
  return mountAgent(PrivacyPage, {
    me: fx.me('admin'),
    api: {
      'GET /admin/privacy/requests': { items: [req(), ready], next_cursor: null },
      'GET /admin/privacy/settings': SETTINGS,
      ...api,
    },
  })
}

describe('admin Privacy page (FE-084/FE-086)', () => {
  it('lists requests with the person, status and the download of a ready export', async () => {
    const h = await mountPage()
    await h.flush()
    const rows = allByTestId('privacy-request-row')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.textContent).toContain('Carla Customer')
    expect(rows[0]!.textContent).toContain('Pending')
    expect(rows[0]!.textContent).toContain('Please forget me')
    const dl = byTestId('privacy-download') as HTMLAnchorElement
    expect(dl.getAttribute('href')).toBe('/api/v1/admin/privacy/exports/r-2')
    expect(dl.hasAttribute('download')).toBe(true)
    // approve/reject only on the pending erasure request
    expect(allByTestId('privacy-approve')).toHaveLength(1)
  })

  it('filters by type and status', async () => {
    const h = await mountPage()
    await h.flush()
    const selects = h.wrapper.findAllComponents({ name: 'VSelect' })
    selects[0]!.vm.$emit('update:modelValue', 'erasure')
    selects[1]!.vm.$emit('update:modelValue', 'pending')
    await h.flush()
    const last = h.calls('GET /admin/privacy/requests').at(-1)!
    expect(last.url.searchParams.get('type')).toBe('erasure')
    expect(last.url.searchParams.get('status')).toBe('pending')
  })

  it('approving needs a mode and the acknowledgement before it erases', async () => {
    const h = await mountPage({ 'POST /admin/privacy/requests/r-1/approve': () => json(RESULT) })
    await h.flush()
    await click(byTestId('privacy-approve'))
    await click(byTestId('erase-submit'))
    await h.flush()
    expect(h.calls('POST /admin/privacy/requests/r-1/approve')).toHaveLength(0)
    expect(document.body.textContent).toContain('This field is required')
    const form = h.wrapper.findComponent(EraseForm)
    model(form).mode = 'anonymize_and_redact'
    model(form).confirm = true
    await h.flush()
    await click(byTestId('erase-submit'))
    await h.flush()
    expect(h.calls('POST /admin/privacy/requests/r-1/approve')[0]!.body).toEqual({ mode: 'anonymize_and_redact' })
    expect(useUiStore().toasts.map((x) => x.text).join()).toContain('3 messages redacted')
  })

  it('rejecting requires a reason', async () => {
    const h = await mountPage({ 'POST /admin/privacy/requests/r-1/reject': (_u, init) => json(req({ status: 'rejected', reason: init.body.reason })) })
    await h.flush()
    await click(byTestId('privacy-reject'))
    await click(byTestId('reject-submit'))
    expect(h.calls('POST /admin/privacy/requests/r-1/reject')).toHaveLength(0)
    await typeInto(byTestId('reject-reason'), 'Legal hold on this account')
    await click(byTestId('reject-submit'))
    await h.flush()
    expect(h.calls('POST /admin/privacy/requests/r-1/reject')[0]!.body).toEqual({ reason: 'Legal hold on this account' })
  })

  it('previews the retention dry run with the unsaved form values', async () => {
    const h = await mountPage({
      'GET /admin/privacy/retention/preview': {
        closed_tickets: 4, closed_tickets_action: 'purge', inbound_emails: 2, audit_entries: 10, expired_exports: 1, erasure_auto_approvals: 0, as_of: ISO,
      },
    })
    await h.flush()
    await click(byTestId('privacy-tab-settings'))
    await typeInto(byTestId('closed-tickets-months'), '24')
    model(h.wrapper.findComponent(PrivacySettingsForm)).closed_tickets_action = 'purge'
    await h.flush()
    await click(byTestId('retention-preview-btn'))
    await h.flush()
    const q = h.calls('GET /admin/privacy/retention/preview')[0]!.url.searchParams
    expect(q.get('closed_tickets_months')).toBe('24')
    expect(q.get('closed_tickets_action')).toBe('purge')
    expect(q.get('inbound_email_days')).toBe('90')
    expect(byTestId('preview-closedTickets')!.textContent).toContain('4')
    expect(h.calls('PUT /admin/privacy/settings')).toHaveLength(0)
  })

  it('blocks out-of-range values, then saves the settings', async () => {
    const h = await mountPage({ 'PUT /admin/privacy/settings': (_u, init) => json(init.body) })
    await h.flush()
    await click(byTestId('privacy-tab-settings'))
    await typeInto(byTestId('inbound-email-days'), '400')
    await click(byTestId('privacy-settings-save'))
    await click(byTestId('retention-preview-btn'))
    expect(document.body.textContent).toContain('Enter a value between 1 and 90')
    expect(h.calls('PUT /admin/privacy/settings')).toHaveLength(0)
    expect(h.calls('GET /admin/privacy/retention/preview')).toHaveLength(0)
    await typeInto(byTestId('inbound-email-days'), '30')
    await typeInto(byTestId('auto-approve-days'), '14')
    await typeInto(byTestId('policy-version'), '2026-10')
    await click(byTestId('privacy-settings-save'))
    await h.flush()
    expect(h.calls('PUT /admin/privacy/settings')[0]!.body).toEqual({
      retention: { ...SETTINGS.retention, inbound_email_days: 30 },
      erasure_auto_approve_days: 14,
      erasure_auto_approve_mode: 'anonymize',
      policy_version: '2026-10',
    })
  })
})
