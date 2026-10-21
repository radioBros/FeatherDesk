// SPDX-License-Identifier: AGPL-3.0-only

import WebhooksPage from './WebhooksPage.vue'
import WebhookForm from './WebhookForm.vue'
import { mountAgent, fx, json, byTestId, typeInto, click } from '../testing'
import { settleConfirm } from '@/composables/useConfirm'
import type { components } from '@/api/schema'

type S = components['schemas']
const hook = (extra: Partial<S['Webhook']> = {}): S['Webhook'] => ({
  id: 'wh-1',
  url: 'https://hooks.example.com/st',
  events: ['ticket.created', 'message.created'],
  include_internal: false,
  active: true,
  consecutive_failures: 0,
  ...extra,
})

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('WebhooksPage (FE-040)', () => {
  it('lists webhooks with status, failures and the disabled reason', async () => {
    const h = await mountAgent(WebhooksPage, {
      me: fx.me('admin'),
      api: { 'GET /admin/webhooks': [hook(), hook({ id: 'wh-2', active: false, consecutive_failures: 12, disabled_reason: 'Too many failures' })] },
    })
    const rows = h.wrapper.findAll('[data-testid="webhook-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[1]!.text()).toContain('Too many failures')
    expect(rows[1]!.text()).toContain('12')
    expect(rows[1]!.find('[data-testid="webhook-reenable"]').exists()).toBe(true)
    expect(rows[0]!.find('[data-testid="webhook-reenable"]').exists()).toBe(false)
  })

  it('shows the secret once after create and removes it from the DOM on close', async () => {
    const SECRET = 'whsec_super_secret_value_123'
    const h = await mountAgent(WebhooksPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/webhooks': [],
        'POST /admin/webhooks': (_u, init) => json({ ...hook(), ...init.body, secret: SECRET }, 201),
      },
    })
    await click(byTestId('webhook-new'))
    await typeInto(byTestId('webhook-url'), 'https://hooks.example.com/new')
    // pick two events through the component model (autocomplete menus are flaky in jsdom)
    const form = h.wrapper.findComponent(WebhookForm)
    ;(form.vm as unknown as { $: { setupState: { model: { events: string[] } } } }).$.setupState.model.events.push('ticket.created', 'note.created')
    await h.flush()
    await click(byTestId('webhook-save'))
    await h.flush()
    expect(h.calls('POST /admin/webhooks')[0]!.body).toMatchObject({ url: 'https://hooks.example.com/new', events: ['ticket.created', 'note.created'] })
    expect(document.body.innerHTML).toContain(SECRET)
    await click(byTestId('webhook-secret-close'))
    await h.flush()
    expect(document.body.innerHTML).not.toContain(SECRET)
    expect((h.wrapper.vm as unknown as { $: { setupState: { secret: string | null } } }).$.setupState.secret).toBeNull()
  })

  it('re-enables an auto-disabled webhook', async () => {
    const h = await mountAgent(WebhooksPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/webhooks': [hook({ active: false, disabled_reason: 'x' })],
        'PATCH /admin/webhooks/wh-1': (_u, init) => json({ ...hook(), ...init.body }),
      },
    })
    await click(byTestId('webhook-reenable'))
    await h.flush()
    expect(h.calls('PATCH /admin/webhooks/wh-1')[0]!.body).toEqual({ active: true })
  })

  it('deletes after confirmation', async () => {
    const h = await mountAgent(WebhooksPage, {
      me: fx.me('admin'),
      api: { 'GET /admin/webhooks': [hook()], 'DELETE /admin/webhooks/wh-1': () => new Response(null, { status: 204 }) },
    })
    await click(byTestId('webhook-delete'))
    settleConfirm(true)
    await h.flush()
    expect(h.calls('DELETE /admin/webhooks/wh-1')).toHaveLength(1)
  })

  it('rotating the secret shows the new secret when the API returns one', async () => {
    const h = await mountAgent(WebhooksPage, {
      me: fx.me('admin'),
      api: {
        'GET /admin/webhooks': [hook()],
        'PATCH /admin/webhooks/wh-1': () => json({ ...hook(), secret: 'rotated-secret-xyz' }),
      },
    })
    await click(byTestId('webhook-rotate'))
    settleConfirm(true)
    await h.flush()
    expect(h.calls('PATCH /admin/webhooks/wh-1')[0]!.body).toEqual({ rotate_secret: true })
    expect(document.body.innerHTML).toContain('rotated-secret-xyz')
  })
})

describe('WebhookForm (FE-040, FE-005)', () => {
  it('rejects an http:// URL client-side and sends nothing', async () => {
    const h = await mountAgent(WebhookForm, {})
    await typeInto(byTestId('webhook-url'), 'http://hooks.example.com/x')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('Enter an https:// URL')
    expect(h.calls()).toHaveLength(0)
  })

  it('Test validates the form first: an invalid form sends no /test request', async () => {
    const h = await mountAgent(WebhookForm, {
      props: { webhook: hook({ url: 'http://insecure.example.com' }) },
      api: { 'POST /admin/webhooks/wh-1/test': { status_code: 200, response_ms: 42 } },
    })
    await click(byTestId('webhook-test'))
    await h.flush()
    expect(h.calls()).toHaveLength(0)
    expect(h.wrapper.text()).toContain('Enter an https:// URL')
  })

  it('Test on a valid webhook calls /test and shows status and latency', async () => {
    const h = await mountAgent(WebhookForm, {
      props: { webhook: hook() },
      api: { 'POST /admin/webhooks/wh-1/test': { status_code: 204, response_ms: 42, error: null } },
    })
    await click(byTestId('webhook-test'))
    await h.flush()
    expect(h.calls('POST /admin/webhooks/wh-1/test')).toHaveLength(1)
    expect(byTestId('webhook-test-result')!.textContent).toContain('204')
    expect(byTestId('webhook-test-result')!.textContent).toContain('42')
  })

  it('shows a warning when internal notes are included', async () => {
    const h = await mountAgent(WebhookForm, { props: { webhook: hook({ include_internal: true }) } })
    expect(h.wrapper.find('[data-testid="webhook-internal-warning"]').exists()).toBe(true)
  })
})
