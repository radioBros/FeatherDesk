// SPDX-License-Identifier: AGPL-3.0-only

import MacroPicker from './MacroPicker.vue'
import TicketDetailPage from '../tickets/TicketDetailPage.vue'
import { mountAgent, fx, json, byTestId, allByTestId, click, type AgentHarness } from '../testing'
import { useUiStore } from '@/stores/ui'
import type { components } from '@/api/schema'

type S = components['schemas']

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const macro = (extra: Partial<S['Macro']> = {}): S['Macro'] => ({
  id: 'm-1',
  name: 'Ask for logs',
  shared: false,
  owner_id: 'u-me',
  reply_kind: 'public',
  reply_md: 'Please send the logs.',
  status: 'pending',
  priority: null,
  add_tags: ['needs-info'],
  project_id: null,
  remove_tags: [],
  assignee: null,
  created_at: fx.iso,
  updated_at: fx.iso,
  ...extra,
})

describe('MacroPicker (FE-065)', () => {
  it('loads the macros only when opened, then applies with an Idempotency-Key and If-Match', async () => {
    const result = { ticket: fx.ticket({ status: 'pending', tags: ['needs-info'] }), visible: true }
    const h = await mountAgent(MacroPicker, {
      props: { ticket: fx.ticket(), etag: 'W/"1"' },
      api: {
        'GET /agent/macros': [macro({ id: 'm-2', name: 'Zebra', shared: true, owner_id: null }), macro()],
        'POST /agent/tickets/t-1/macros/m-1/apply': () => new Response(JSON.stringify(result), { status: 200, headers: { 'Content-Type': 'application/json', ETag: 'W/"2"' } }),
      },
    })
    expect(h.calls('GET /agent/macros')).toHaveLength(0)
    await click(byTestId('macro-picker'))
    await h.flush()
    const items = allByTestId('macro-item')
    expect(items.map((i) => i.textContent?.includes('Ask for logs'))).toEqual([true, false])
    await click(items[0]!)
    await h.flush()
    const call = h.calls('POST /agent/tickets/t-1/macros/m-1/apply')[0]!
    expect(call.headers.get('Idempotency-Key')).toBeTruthy()
    expect(call.headers.get('If-Match')).toBe('W/"1"')
    const [res, etag] = h.wrapper.emitted('applied')![0] as [S['TicketUpdateResult'], string]
    expect(res.ticket.status).toBe('pending')
    expect(etag).toBe('W/"2"')
  })

  it('without an ETag no If-Match is sent; a 403 is reported to the page', async () => {
    const h = await mountAgent(MacroPicker, {
      props: { ticket: fx.ticket(), etag: null },
      api: {
        'GET /agent/macros': [macro()],
        'POST /agent/tickets/t-1/macros/m-1/apply': json({ type: 'https://featherdesk.dev/problems/forbidden', title: 'Forbidden', status: 403 }, 403),
      },
    })
    await click(byTestId('macro-picker'))
    await h.flush()
    await click(byTestId('macro-item'))
    await h.flush()
    expect(h.calls('POST /agent/tickets/t-1/macros/m-1/apply')[0]!.headers.get('If-Match')).toBeNull()
    expect(h.wrapper.emitted('failed')).toHaveLength(1)
  })
})

describe('TicketDetailPage with macros (FE-065)', () => {
  const lookups = {
    'GET /agent/teams': [fx.team()],
    'GET /agent/categories': [],
    'GET /agent/custom-fields': [],
    'GET /agent/tags': [],
    'GET /agent/staff': [],
    'GET /agent/tickets/t-1': fx.ticket(),
  }
  async function open(api: Record<string, unknown>): Promise<AgentHarness> {
    return mountAgent(TicketDetailPage, {
      path: '/agent/tickets/:number',
      route: '/agent/tickets/1042',
      api: { ...lookups, 'GET /agent/tickets/by-number/1042': fx.ticket(), 'GET /agent/macros': [macro()], ...api } as never,
    })
  }

  it('applying a macro updates the ticket and confirms with a toast', async () => {
    const h = await open({ 'POST /agent/tickets/t-1/macros/m-1/apply': { ticket: fx.ticket({ status: 'pending', subject: 'Printer on fire' }), visible: true } })
    await click(byTestId('macro-picker'))
    await h.flush()
    await click(byTestId('macro-item'))
    await h.flush()
    expect(useUiStore().toasts.at(-1)?.text).toBe('Macro “Ask for logs” applied')
    expect(h.router.currentRoute.value.path).toBe('/agent/tickets/1042')
  })

  it('visible: false navigates back to the list', async () => {
    const h = await open({ 'POST /agent/tickets/t-1/macros/m-1/apply': { ticket: fx.ticket(), visible: false } })
    await click(byTestId('macro-picker'))
    await h.flush()
    await click(byTestId('macro-item'))
    await h.flush()
    expect(h.router.currentRoute.value.path).toBe('/agent/tickets')
    expect(useUiStore().toasts.at(-1)?.text).toBe('You no longer have access to this ticket.')
  })

  it('a 409 problem shows as an error toast and keeps the ticket', async () => {
    const h = await open({
      'POST /agent/tickets/t-1/macros/m-1/apply': json({ type: 'https://featherdesk.dev/problems/invalid-transition', title: 'Invalid transition', status: 409, allowed: ['open'] }, 409),
    })
    await click(byTestId('macro-picker'))
    await h.flush()
    await click(byTestId('macro-item'))
    await h.flush()
    expect(useUiStore().toasts.at(-1)?.color).toBe('error')
    expect(h.router.currentRoute.value.path).toBe('/agent/tickets/1042')
  })
})
