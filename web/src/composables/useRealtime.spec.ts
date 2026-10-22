// SPDX-License-Identifier: AGPL-3.0-only

import { defineComponent, h } from 'vue'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { useRealtime } from './useRealtime'
import { useRealtimeStore } from '@/stores/realtime'
import { useSessionStore } from '@/stores/session'
import { isStaffOnlyTopic, type WebSocketLike } from '@/realtime/client'
import type { Me } from '@/types'

class Sock implements WebSocketLike {
  readyState = 1
  sent: { op: string; topics?: string[] }[] = []
  onopen: ((ev: unknown) => void) | null = null
  onclose: ((ev: { code: number }) => void) | null = null
  onmessage: ((ev: { data: unknown }) => void) | null = null
  onerror: ((ev: unknown) => void) | null = null
  send(data: string) {
    this.sent.push(JSON.parse(data))
  }
  close() {}
}

const ALL = ['me', 'tickets', 'ticket:t1', 'counts', 'kb', 'queue:team-1', 'admin:system']

function subscribed(kind: 'customer' | 'staff') {
  const pinia = createPinia()
  setActivePinia(pinia)
  useSessionStore().me = { id: 'u1', kind, role: kind === 'staff' ? 'admin' : 'requester', name: 'U', email: 'u@x', csrf_token: 'c' } as Me
  const sock = new Sock()
  useRealtimeStore().start({ createSocket: () => sock })
  sock.onopen?.({})
  mount(defineComponent({ setup: () => (useRealtime(ALL), () => h('div')) }), { global: { plugins: [pinia] } })
  return sock.sent.filter((f) => f.op === 'subscribe').flatMap((f) => f.topics ?? [])
}

describe('useRealtime topic guard (CON-013)', () => {
  it('knows the staff-only topics of x-websocket', () => {
    expect(ALL.filter(isStaffOnlyTopic)).toEqual(['counts', 'kb', 'queue:team-1', 'admin:system'])
  })

  it('a customer never subscribes to counts, kb, queue:* or admin:* (the server closes with 4403)', () => {
    expect(subscribed('customer')).toEqual(['me', 'tickets', 'ticket:t1'])
  })

  it('staff subscribe to every topic they ask for', () => {
    expect(subscribed('staff')).toEqual(ALL)
  })
})
