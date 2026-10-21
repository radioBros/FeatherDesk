// SPDX-License-Identifier: AGPL-3.0-only

import { setActivePinia, createPinia } from 'pinia'
import { useRealtimeStore } from './realtime'
import type { WebSocketLike } from '@/realtime/client'

beforeEach(() => setActivePinia(createPinia()))

function fakeSocket(): WebSocketLike & { open(): void } {
  const s = {
    readyState: 0,
    send: vi.fn(),
    close: vi.fn(),
    onopen: null as ((e: unknown) => void) | null,
    onclose: null as ((e: { code: number }) => void) | null,
    onmessage: null,
    onerror: null,
    open() {
      s.readyState = 1
      s.onopen?.({})
    },
  }
  return s
}

describe('realtime store (FE-044)', () => {
  it('keeps one client per tab and tracks connection state', () => {
    const sock = fakeSocket()
    const rt = useRealtimeStore()
    const c1 = rt.start({ createSocket: () => sock })
    const c2 = rt.start({ createSocket: () => fakeSocket() })
    expect(c1).toBe(c2)
    expect(rt.connected).toBe(false)
    sock.open()
    expect(rt.connected).toBe(true)
    expect(rt.everConnected).toBe(true)
    rt.stop()
    expect(rt.client).toBeNull()
    expect(rt.connected).toBe(false)
  })
  it('pauses non-essential topics when the document is hidden', () => {
    const sock = fakeSocket()
    const rt = useRealtimeStore()
    const c = rt.start({ createSocket: () => sock })
    sock.open()
    c.subscribe('counts')
    Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true })
    document.dispatchEvent(new Event('visibilitychange'))
    const last = JSON.parse((sock.send as any).mock.calls.at(-1)[0])
    expect(last).toMatchObject({ op: 'unsubscribe', topics: ['counts'] })
    Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
    rt.stop()
  })
})
