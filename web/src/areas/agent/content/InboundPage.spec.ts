// SPDX-License-Identifier: AGPL-3.0-only

import InboundPage from './InboundPage.vue'
import InboundMailboxForm from './InboundMailboxForm.vue'
import { mountAgent, fx, json, byTestId, typeInto, click } from '../testing'
import type { components } from '@/api/schema'

type Mailbox = components['schemas']['InboundMailbox']

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const imap = (extra: Partial<Mailbox> = {}, imapExtra: Partial<NonNullable<Mailbox['imap']>> = {}): Mailbox => ({
  id: 'mb-1',
  name: 'Support inbox',
  kind: 'imap',
  address: 'support@example.com',
  active: true,
  team_id: 'team-1',
  last_ok_at: fx.iso,
  imap: {
    host: 'imap.example.com',
    port: 993,
    security: 'tls',
    auth: 'password',
    username: 'support',
    folder: 'INBOX',
    processed_action: 'move',
    processed_folder: 'Done',
    password_set: true,
    ...imapExtra,
  },
  ...extra,
})

const input = (id: string) => byTestId(id)?.querySelector('input') as HTMLInputElement

describe('InboundPage (FE-057)', () => {
  it('lists mailboxes with team, status and last error', async () => {
    const h = await mountAgent(InboundPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/inbound/mailboxes': [imap(), imap({ id: 'mb-2', name: 'Broken', team_id: null, last_error: 'auth failed', last_ok_at: null })],
        'GET /agent/teams': [fx.team()],
      },
    })
    await h.flush()
    const rows = h.wrapper.findAll('[data-testid="inbound-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.text()).toContain('Support')
    expect(rows[1]!.text()).toContain('Triage')
    expect(rows[1]!.text()).toContain('auth failed')
  })

  it('shows the webhook ingest URL once; after closing it is gone from the DOM', async () => {
    const url = 'https://help.example.com/api/v1/inbound/mime/tok_SECRET123'
    const h = await mountAgent(InboundPage, {
      me: fx.me('admin'),
      api: { 'GET /admin/inbound/mailboxes': [], 'GET /agent/teams': [] },
    })
    await click(byTestId('inbound-new'))
    h.wrapper.findComponent(InboundMailboxForm).vm.$emit('saved', { ...imap(), kind: 'webhook', imap: null, webhook_url: url }, true)
    await h.flush()
    expect(document.body.innerHTML).toContain('tok_SECRET123')
    await click(byTestId('secret-close'))
    await h.flush()
    await new Promise((r) => setTimeout(r, 50))
    expect(document.body.innerHTML).not.toContain('tok_SECRET123')
    expect(h.wrapper.html()).not.toContain('tok_SECRET123')
  })
})

describe('InboundMailboxForm (FE-057, FE-005)', () => {
  it('never pre-fills the password and leaves it out of the PATCH when untouched', async () => {
    const h = await mountAgent(InboundMailboxForm, {
      props: { mailbox: imap(), teams: [] },
      api: { 'PATCH /admin/inbound/mailboxes/mb-1': (_u, i) => json({ ...imap(), ...(i.body as object) }) },
    })
    expect(input('inbound-password').value).toBe('')
    expect(byTestId('password-set')).not.toBeNull()
    await click(byTestId('inbound-save'))
    await h.flush()
    const body = h.calls('PATCH /admin/inbound/mailboxes/mb-1')[0]!.body as { imap: Record<string, unknown> }
    expect(body.imap.host).toBe('imap.example.com')
    expect('password' in body.imap).toBe(false)
  })

  it('sends a newly typed password', async () => {
    const h = await mountAgent(InboundMailboxForm, {
      props: { mailbox: imap(), teams: [] },
      api: { 'PATCH /admin/inbound/mailboxes/mb-1': (_u, i) => json({ ...imap(), ...(i.body as object) }) },
    })
    await typeInto(byTestId('inbound-password'), 'n3w-secret')
    await click(byTestId('inbound-save'))
    await h.flush()
    expect((h.calls('PATCH /admin/inbound/mailboxes/mb-1')[0]!.body as { imap: { password: string } }).imap.password).toBe('n3w-secret')
  })

  it('env-locked fields are disabled, badged and omitted from the body', async () => {
    const h = await mountAgent(InboundMailboxForm, {
      props: { mailbox: imap({ locked_by_env: ['imap.host', 'imap.password'] }), teams: [] },
      api: { 'PATCH /admin/inbound/mailboxes/mb-1': (_u, i) => json({ ...imap(), ...(i.body as object) }) },
    })
    expect(input('inbound-host').disabled).toBe(true)
    expect(byTestId('inbound-host')?.querySelector('[data-testid="env-lock"]')).not.toBeNull()
    expect(input('inbound-password').disabled).toBe(true)
    await click(byTestId('inbound-save'))
    await h.flush()
    const body = h.calls('PATCH /admin/inbound/mailboxes/mb-1')[0]!.body as { imap: Record<string, unknown> }
    expect('host' in body.imap).toBe(false)
    expect('password' in body.imap).toBe(false)
    expect(body.imap.username).toBe('support')
  })

  it('an empty host blocks both Save and Test connection', async () => {
    const h = await mountAgent(InboundMailboxForm, {
      props: { mailbox: imap({}, { host: '' }), teams: [] },
      api: { 'PATCH /admin/inbound/mailboxes/mb-1': json(imap()), 'POST /admin/inbound/mailboxes/mb-1/test': json({ ok: true }) },
    })
    await click(byTestId('inbound-save'))
    await click(byTestId('inbound-test'))
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })

  it('Test connection validates, then reports the result in plain words', async () => {
    const h = await mountAgent(InboundMailboxForm, {
      props: { mailbox: imap(), teams: [] },
      api: { 'POST /admin/inbound/mailboxes/mb-1/test': json({ ok: false, error: 'LOGIN failed: invalid credentials' }) },
    })
    await click(byTestId('inbound-test'))
    await h.flush()
    expect(h.calls('POST /admin/inbound/mailboxes/mb-1/test')).toHaveLength(1)
    expect(byTestId('inbound-test-result')?.textContent).toContain('Connection failed: LOGIN failed: invalid credentials')
  })

  it('an empty create form sends nothing', async () => {
    const h = await mountAgent(InboundMailboxForm, { props: { teams: [] } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })
})
