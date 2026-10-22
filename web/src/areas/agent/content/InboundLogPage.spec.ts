// SPDX-License-Identifier: AGPL-3.0-only

import InboundLogPage from './InboundLogPage.vue'
import { mountAgent, fx, json, click } from '../testing'
import { settleConfirm } from '@/composables/useConfirm'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const msg = (id: string, status: string, extra: Record<string, unknown> = {}) => ({
  id,
  mailbox_id: 'mb-1',
  message_id: `<${id}@mail>`,
  from_addr: 'ada@example.com',
  subject: `Subject ${id}`,
  received_at: fx.iso,
  status,
  reason: status === 'quarantined' ? 'sender not allowed' : null,
  ...extra,
})

describe('InboundLogPage (FE-057)', () => {
  it('lists messages and offers Retry only for failed / quarantined', async () => {
    const h = await mountAgent(InboundLogPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/inbound/messages': fx.page([msg('1', 'processed', { ticket_id: 't-1' }), msg('2', 'quarantined'), msg('3', 'failed')]),
        'GET /admin/inbound/mailboxes': [{ id: 'mb-1', name: 'Support inbox', kind: 'imap', address: 'support@example.com', active: true }],
      },
    })
    await h.flush()
    const rows = h.wrapper.findAll('[data-testid="inlog-row"]')
    expect(rows).toHaveLength(3)
    expect(rows[0]!.text()).toContain('Support inbox')
    expect(rows[0]!.find('[data-testid="inlog-retry"]').exists()).toBe(false)
    expect(rows[0]!.find('[data-testid="inlog-ticket"]').exists()).toBe(true)
    expect(rows[1]!.text()).toContain('sender not allowed')
    expect(h.wrapper.findAll('[data-testid="inlog-retry"]')).toHaveLength(2)
  })

  it('Retry posts after confirm and the row updates from the response', async () => {
    const h = await mountAgent(InboundLogPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/inbound/messages': fx.page([msg('2', 'quarantined')]),
        'GET /admin/inbound/mailboxes': [],
        'POST /admin/inbound/messages/2/retry': json(msg('2', 'processed', { ticket_id: 't-9', reason: null })),
      },
    })
    await click(h.wrapper.find('[data-testid="inlog-retry"]').element)
    expect(h.calls('POST /admin/inbound/messages/2/retry')).toHaveLength(0)
    settleConfirm(true)
    await h.flush()
    expect(h.calls('POST /admin/inbound/messages/2/retry')).toHaveLength(1)
    const row = h.wrapper.find('[data-testid="inlog-row"]')
    expect(row.find('[data-testid="inlog-status-chip"]').text()).toBe('Processed')
    expect(row.find('[data-testid="inlog-retry"]').exists()).toBe(false)
  })

  it('a cancelled confirm sends nothing', async () => {
    const h = await mountAgent(InboundLogPage, {
      me: fx.me('admin'),
      api: { 'GET /admin/inbound/messages': fx.page([msg('3', 'failed')]), 'GET /admin/inbound/mailboxes': [] },
    })
    await click(h.wrapper.find('[data-testid="inlog-retry"]').element)
    settleConfirm(false)
    await h.flush()
    expect(h.calls(/^POST/)).toHaveLength(0)
  })
})
