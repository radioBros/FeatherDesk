// SPDX-License-Identifier: AGPL-3.0-only

import PrivacyActions from './PrivacyActions.vue'
import EraseForm from './EraseForm.vue'
import CustomerDetailPage from '../directory/CustomerDetailPage.vue'
import { mountAgent, fx, json, byTestId, click } from '../testing'
import type { components } from '@/api/schema'

type S = components['schemas']
afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const USER = { id: 'u-c', name: 'Carla Customer' }
const RESULT: S['ErasureResult'] = {
  user_id: 'u-c',
  mode: 'anonymize',
  name: 'Deleted user #7',
  tickets_kept: 1,
  messages_redacted: 0,
  attachments_deleted: 0,
  field_values_redacted: 0,
  request: {
    id: 'r-9', type: 'erasure', status: 'completed', user_id: 'u-c', requested_by: 'u-me', include_internal: false, mode: 'anonymize',
    file_size: 0, reason: '', error: '', decided_by: 'u-me', decided_at: null, completed_at: null, expires_at: null, download_url: null,
    created_at: '2026-10-20T10:00:00Z',
  },
}

describe('PrivacyActions (FE-087)', () => {
  it('renders nothing and requests nothing for non-admins', async () => {
    const h = await mountAgent(PrivacyActions, { me: fx.me('supervisor'), props: { user: USER } })
    await h.flush()
    expect(byTestId('privacy-actions')).toBeNull()
    expect(h.calls()).toEqual([])
  })

  it('exports the user with the include-internal option and lists their requests', async () => {
    const h = await mountAgent(PrivacyActions, {
      me: fx.me('admin'),
      props: { user: USER },
      api: {
        'GET /admin/privacy/requests': { items: [], next_cursor: null },
        'POST /admin/privacy/users/u-c/export': () => json({ ...RESULT.request, type: 'export', status: 'pending' }, 202),
      },
    })
    await h.flush()
    expect(h.calls('GET /admin/privacy/requests')[0]!.url.searchParams.get('user_id')).toBe('u-c')
    expect(byTestId('privacy-actions-empty')).not.toBeNull()
    await click(byTestId('privacy-include-internal')!.querySelector('input'))
    await click(byTestId('privacy-export'))
    await h.flush()
    expect(h.calls('POST /admin/privacy/users/u-c/export')[0]!.body).toEqual({ include_internal: true })
    expect(h.calls('GET /admin/privacy/requests')).toHaveLength(2)
  })

  it('erases through the confirm dialog with the chosen mode', async () => {
    const h = await mountAgent(PrivacyActions, {
      me: fx.me('admin'),
      props: { user: USER },
      api: { 'GET /admin/privacy/requests': { items: [], next_cursor: null }, 'POST /admin/privacy/users/u-c/erase': () => json(RESULT) },
    })
    await h.flush()
    await click(byTestId('privacy-erase'))
    await click(byTestId('erase-submit'))
    expect(h.calls('POST /admin/privacy/users/u-c/erase')).toHaveLength(0)
    const m = (h.wrapper.findComponent(EraseForm).vm as unknown as { $: { setupState: { model: { mode: string; confirm: boolean } } } }).$.setupState.model
    m.mode = 'anonymize'
    m.confirm = true
    await h.flush()
    await click(byTestId('erase-submit'))
    await h.flush()
    expect(h.calls('POST /admin/privacy/users/u-c/erase')[0]!.body).toEqual({ mode: 'anonymize' })
    expect(h.wrapper.emitted()).toBeTruthy()
  })

  it('is part of the customer detail view', async () => {
    const h = await mountAgent(CustomerDetailPage, {
      path: '/agent/customers/:id',
      route: '/agent/customers/u-c',
      me: fx.me('admin'),
      api: {
        'GET /agent/customers/u-c': fx.user({ id: 'u-c', kind: 'customer', role: 'requester', name: 'Carla Customer' }),
        'GET /agent/organizations': { items: [], next_cursor: null },
        'GET /agent/tickets': { items: [], next_cursor: null },
        'GET /admin/privacy/requests': { items: [], next_cursor: null },
      },
    })
    await h.flush()
    expect(byTestId('privacy-actions')).not.toBeNull()
  })
})
