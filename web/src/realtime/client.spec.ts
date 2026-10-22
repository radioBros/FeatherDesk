// SPDX-License-Identifier: AGPL-3.0-only

import { RealtimeClient, backoffDelay, SUBPROTOCOL, type WebSocketLike } from './client'

class MockSocket implements WebSocketLike {
  static all: MockSocket[] = []
  readyState = 0
  sent: any[] = []
  onopen: ((ev: unknown) => void) | null = null
  onclose: ((ev: { code: number }) => void) | null = null
  onmessage: ((ev: { data: unknown }) => void) | null = null
  onerror: ((ev: unknown) => void) | null = null
  constructor(public url: string, public protocol: string) {
    MockSocket.all.push(this)
  }
  send(data: string) {
    this.sent.push(JSON.parse(data))
  }
  close(code = 1000) {
    this.readyState = 3
    this.onclose?.({ code })
  }
  open() {
    this.readyState = 1
    this.onopen?.({})
  }
  push(frame: unknown) {
    this.onmessage?.({ data: JSON.stringify(frame) })
  }
  serverClose(code: number) {
    this.readyState = 3
    this.onclose?.({ code })
  }
}

function make() {
  MockSocket.all = []
  const timers: { fn: () => void; ms: number }[] = []
  const c = new RealtimeClient({
    url: 'ws://test/api/v1/ws',
    createSocket: (u, p) => new MockSocket(u, p),
    random: () => 0.5,
    setTimer: (fn, ms) => {
      timers.push({ fn, ms })
      return timers.length
    },
    clearTimer: () => {},
  })
  return { c, timers, sock: () => MockSocket.all[MockSocket.all.length - 1]! }
}

describe('RealtimeClient (FE-044)', () => {
  it('connects with the featherdesk.v1 subprotocol and subscribes on open', () => {
    const { c, sock } = make()
    c.subscribe('me', { essential: true })
    c.start()
    expect(sock().protocol).toBe(SUBPROTOCOL)
    sock().open()
    expect(sock().sent[0]).toMatchObject({ op: 'subscribe', topics: ['me'] })
  })

  it('ref-counts subscriptions and unsubscribes on the last release', () => {
    const { c, sock } = make()
    c.start()
    sock().open()
    const a = c.subscribe('tickets')
    const b = c.subscribe('tickets')
    expect(sock().sent.filter((f) => f.op === 'subscribe')).toHaveLength(1)
    a()
    expect(sock().sent.some((f) => f.op === 'unsubscribe')).toBe(false)
    b()
    expect(sock().sent.at(-1)).toMatchObject({ op: 'unsubscribe', topics: ['tickets'] })
    b()
    expect(sock().sent.filter((f) => f.op === 'unsubscribe')).toHaveLength(1)
  })

  it('reconnects with jittered backoff, resubscribes and resumes from the last event id', () => {
    const { c, sock, timers } = make()
    c.subscribe('tickets')
    c.start()
    sock().open()
    const events: string[] = []
    c.onEvent((e) => events.push(e.event))
    sock().push({ type: 'event', topic: 'tickets', event_id: 'e42', event: 'ticket.created', data: {} })
    expect(events).toEqual(['ticket.created'])
    sock().serverClose(1006)
    expect(timers).toHaveLength(1)
    expect(timers[0]!.ms).toBe(backoffDelay(0, () => 0.5))
    timers[0]!.fn()
    sock().open()
    expect(sock().sent).toEqual(
      expect.arrayContaining([expect.objectContaining({ op: 'subscribe', topics: ['tickets'] }), expect.objectContaining({ op: 'resume', last_event_id: 'e42' })]),
    )
  })

  it('backoff grows from 1 s and caps at 30 s', () => {
    expect(backoffDelay(0, () => 0)).toBe(1000)
    expect(backoffDelay(3, () => 1)).toBe(8000)
    expect(backoffDelay(20, () => 1)).toBe(30000)
    expect(backoffDelay(20, () => 0)).toBe(15000)
  })

  it('does not reconnect after 4401 (unauthenticated)', () => {
    const { c, sock, timers } = make()
    const codes: number[] = []
    c.onClose((code) => codes.push(code))
    c.start()
    sock().open()
    sock().serverClose(4401)
    expect(codes).toEqual([4401])
    expect(timers).toHaveLength(0)
  })

  it('resync frames call the refetch callbacks; counts frames reach counts handlers', () => {
    const { c, sock } = make()
    const resync = vi.fn()
    const counts = vi.fn()
    c.onResync(resync)
    c.onCounts(counts)
    c.start()
    sock().open()
    sock().push({ type: 'resync', reason: 'gap_too_large' })
    sock().push({ type: 'counts', data: { mine: 3 } })
    expect(resync).toHaveBeenCalledWith('gap_too_large')
    expect(counts).toHaveBeenCalledWith({ mine: 3 })
  })

  it('ticket.access_lost and denied acks drop the subscription', () => {
    const { c, sock } = make()
    c.start()
    sock().open()
    c.subscribe('ticket:1')
    c.subscribe('ticket:2')
    sock().push({ type: 'event', topic: 'ticket:1', event_id: 'e1', event: 'ticket.access_lost' })
    sock().push({ type: 'ack', id: 'c2', ok: true, denied: ['ticket:2'] })
    expect(c.topics()).toEqual([])
  })

  it('pauses non-essential topics while hidden and resyncs on return', () => {
    const { c, sock } = make()
    const resync = vi.fn()
    c.onResync(resync)
    c.start()
    sock().open()
    c.subscribe('me', { essential: true })
    c.subscribe('counts')
    c.setHidden(true)
    expect(sock().sent.at(-1)).toMatchObject({ op: 'unsubscribe', topics: ['counts'] })
    c.subscribe('tickets') // while hidden: not sent yet
    expect(sock().sent.at(-1)).toMatchObject({ op: 'unsubscribe' })
    c.setHidden(false)
    expect(sock().sent.at(-1)).toMatchObject({ op: 'subscribe', topics: ['counts', 'tickets'] })
    expect(resync).toHaveBeenCalledWith('tab_visible')
  })

  it('stop closes the socket without reconnecting', () => {
    const { c, sock, timers } = make()
    c.start()
    sock().open()
    c.stop()
    expect(timers).toHaveLength(0)
    expect(c.connected).toBe(false)
  })

  it('ignores malformed frames', () => {
    const { c, sock } = make()
    c.start()
    sock().open()
    expect(() => sock().onmessage?.({ data: 'not json' })).not.toThrow()
  })
})
