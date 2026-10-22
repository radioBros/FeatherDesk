// SPDX-License-Identifier: AGPL-3.0-only

/**
 * WebSocket realtime client (FE-044, realtime spec v1). Framework-free so it
 * can be unit-tested with a mock socket; the Pinia store wraps one instance.
 *
 * - one socket per tab, subprotocol `featherdesk.v1`
 * - ref-counted topic subscriptions, re-sent after every reconnect
 * - jittered exponential reconnect 1 s → 30 s
 * - `resume` from the last seen event_id; `resync` → registered refetch callbacks
 * - hidden tab: non-essential topics are paused, re-subscribed (and resynced) on return
 */
import type { components } from '@/api/schema'

export const SUBPROTOCOL = 'featherdesk.v1'

/**
 * Topics only staff may subscribe to (`x-websocket` on GET /ws). A customer
 * subscribing to one is closed with 4403, which ends realtime for the whole
 * tab, so the portal never sends them (CON-013).
 */
export function isStaffOnlyTopic(topic: string): boolean {
  return topic === 'counts' || topic === 'kb' || topic.startsWith('queue:') || topic.startsWith('admin:')
}

// Frame shapes come from the OpenAPI components (CON-013): a change to
// `WsClientFrame` / `WsServerFrame` in api/openapi.yaml regenerates schema.d.ts
// (`pnpm gen:api`) and breaks the typecheck wherever this client or its users
// rely on the old shape.
export type WsClientFrame = components['schemas']['WsClientFrame']
export type WsServerFrame = components['schemas']['WsServerFrame']
type FrameOf<T extends WsServerFrame['type']> = Extract<WsServerFrame, { type: T }>
/** Removes `id` from every member of a union (the client numbers its own frames). */
type WithoutId<T> = T extends unknown ? Omit<T, 'id'> : never
export type OutgoingFrame = WithoutId<WsClientFrame>

export type ServerEvent = FrameOf<'event'>
/** @deprecated use WsServerFrame */
export type ServerFrame = WsServerFrame
export type TicketCounts = FrameOf<'counts'>['data']
export type PresenceUser = FrameOf<'presence'>['users'][number]
export type PresenceState = Extract<WsClientFrame, { op: 'presence' }>['state']
/** Server resync reasons, plus `tab_visible` raised by this client when a hidden tab returns. */
export type ResyncReason = FrameOf<'resync'>['reason'] | 'tab_visible'

export type EventHandler = (ev: ServerEvent) => void
export type CountsHandler = (data: TicketCounts) => void
export type ResyncHandler = (reason: ResyncReason) => void
export type CloseHandler = (code: number) => void
export type PresenceHandler = (topic: string, users: PresenceUser[]) => void

export interface WebSocketLike {
  readyState: number
  send(data: string): void
  close(code?: number, reason?: string): void
  onopen: ((ev: unknown) => void) | null
  onclose: ((ev: { code: number }) => void) | null
  onmessage: ((ev: { data: unknown }) => void) | null
  onerror: ((ev: unknown) => void) | null
}

export interface RealtimeOptions {
  url?: string
  createSocket?: (url: string, protocol: string) => WebSocketLike
  random?: () => number
  setTimer?: (fn: () => void, ms: number) => unknown
  clearTimer?: (h: unknown) => void
  minDelay?: number
  maxDelay?: number
}

const OPEN = 1

export function defaultUrl(): string {
  if (typeof location === 'undefined') return 'ws://localhost/api/v1/ws'
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}/api/v1/ws`
}

export function backoffDelay(attempt: number, random: () => number, min = 1000, max = 30000): number {
  const base = Math.min(max, min * 2 ** Math.max(0, attempt))
  // full jitter between 50% and 100% of the step, never below min
  return Math.max(min, Math.round(base * (0.5 + random() * 0.5)))
}

interface Sub {
  count: number
  essential: boolean
}

export class RealtimeClient {
  readonly url: string
  private opts: Required<Omit<RealtimeOptions, 'url'>>
  private ws: WebSocketLike | null = null
  private subs = new Map<string, Sub>()
  private eventHandlers = new Set<EventHandler>()
  private countsHandlers = new Set<CountsHandler>()
  private resyncHandlers = new Set<ResyncHandler>()
  private closeHandlers = new Set<CloseHandler>()
  private statusHandlers = new Set<(connected: boolean) => void>()
  private presenceHandlers = new Set<PresenceHandler>()
  private attempt = 0
  private timer: unknown = null
  private seq = 0
  private stopped = true
  private hidden = false
  lastEventId: string | null = null
  connected = false

  constructor(options: RealtimeOptions = {}) {
    this.url = options.url ?? defaultUrl()
    this.opts = {
      createSocket:
        options.createSocket ?? ((url, protocol) => new WebSocket(url, protocol) as unknown as WebSocketLike),
      random: options.random ?? Math.random,
      setTimer: options.setTimer ?? ((fn, ms) => setTimeout(fn, ms)),
      clearTimer: options.clearTimer ?? ((h) => clearTimeout(h as ReturnType<typeof setTimeout>)),
      minDelay: options.minDelay ?? 1000,
      maxDelay: options.maxDelay ?? 30000,
    }
  }

  start() {
    if (!this.stopped) return
    this.stopped = false
    this.connect()
  }

  stop() {
    this.stopped = true
    if (this.timer) this.opts.clearTimer(this.timer)
    this.timer = null
    const ws = this.ws
    this.ws = null
    if (ws) {
      ws.onclose = null
      ws.close(1000, 'client stop')
    }
    this.setConnected(false)
  }

  /** Subscribes to a topic; returns the unsubscribe function (ref-counted). */
  subscribe(topic: string, opts: { essential?: boolean } = {}): () => void {
    const sub = this.subs.get(topic)
    if (sub) {
      sub.count++
      sub.essential ||= !!opts.essential
    } else {
      this.subs.set(topic, { count: 1, essential: !!opts.essential })
      if (!this.hidden || opts.essential) this.send({ op: 'subscribe', topics: [topic] })
    }
    let done = false
    return () => {
      if (done) return
      done = true
      const s = this.subs.get(topic)
      if (!s) return
      if (--s.count <= 0) {
        this.subs.delete(topic)
        this.send({ op: 'unsubscribe', topics: [topic] })
      }
    }
  }

  topics(): string[] {
    return [...this.subs.keys()]
  }

  onEvent(fn: EventHandler) {
    this.eventHandlers.add(fn)
    return () => this.eventHandlers.delete(fn)
  }
  onCounts(fn: CountsHandler) {
    this.countsHandlers.add(fn)
    return () => this.countsHandlers.delete(fn)
  }
  onResync(fn: ResyncHandler) {
    this.resyncHandlers.add(fn)
    return () => this.resyncHandlers.delete(fn)
  }
  onClose(fn: CloseHandler) {
    this.closeHandlers.add(fn)
    return () => this.closeHandlers.delete(fn)
  }
  /** Staff-only presence on `ticket:<id>` (realtime spec §2, FE-046). */
  onPresence(fn: PresenceHandler) {
    this.presenceHandlers.add(fn)
    return () => this.presenceHandlers.delete(fn)
  }

  /** Sends a presence op; returns false when the socket is not open (nothing is queued). */
  sendPresence(topic: string, state: PresenceState): boolean {
    return this.send({ op: 'presence', topic, state })
  }

  onStatus(fn: (connected: boolean) => void) {
    this.statusHandlers.add(fn)
    return () => this.statusHandlers.delete(fn)
  }

  /** Tab visibility: pause non-essential topics while hidden. */
  setHidden(hidden: boolean) {
    if (hidden === this.hidden) return
    this.hidden = hidden
    const paused = [...this.subs.entries()].filter(([, s]) => !s.essential).map(([t]) => t)
    if (!paused.length) return
    if (hidden) this.send({ op: 'unsubscribe', topics: paused })
    else {
      this.send({ op: 'subscribe', topics: paused })
      this.emitResync('tab_visible')
    }
  }

  private activeTopics(): string[] {
    return [...this.subs.entries()].filter(([, s]) => !this.hidden || s.essential).map(([t]) => t)
  }

  private connect() {
    if (this.stopped) return
    let ws: WebSocketLike
    try {
      ws = this.opts.createSocket(this.url, SUBPROTOCOL)
    } catch {
      this.scheduleReconnect()
      return
    }
    this.ws = ws
    ws.onopen = () => {
      this.attempt = 0
      this.setConnected(true)
      const topics = this.activeTopics()
      if (topics.length) this.send({ op: 'subscribe', topics })
      if (this.lastEventId) this.send({ op: 'resume', last_event_id: this.lastEventId })
    }
    ws.onmessage = (ev) => this.handle(ev.data)
    ws.onerror = () => {}
    ws.onclose = (ev) => {
      this.ws = null
      this.setConnected(false)
      for (const h of this.closeHandlers) h(ev.code)
      if (ev.code === 4401 || ev.code === 4403) {
        // Unauthenticated or forbidden: the session store decides what to do; no reconnect loop.
        this.stopped = true
        return
      }
      this.scheduleReconnect()
    }
  }

  private scheduleReconnect() {
    if (this.stopped) return
    const delay = backoffDelay(this.attempt++, this.opts.random, this.opts.minDelay, this.opts.maxDelay)
    this.timer = this.opts.setTimer(() => {
      this.timer = null
      this.connect()
    }, delay)
  }

  private setConnected(v: boolean) {
    if (this.connected === v) return
    this.connected = v
    for (const h of this.statusHandlers) h(v)
  }

  private send(frame: OutgoingFrame) {
    const ws = this.ws
    if (!ws || ws.readyState !== OPEN) return false
    ws.send(JSON.stringify({ id: `c${++this.seq}`, ...frame }))
    return true
  }

  private emitResync(reason: ResyncReason) {
    for (const h of this.resyncHandlers) h(reason)
  }

  private handle(raw: unknown) {
    let f: WsServerFrame
    try {
      f = JSON.parse(String(raw)) as WsServerFrame
    } catch {
      return
    }
    switch (f.type) {
      case 'event':
        if (f.event_id) this.lastEventId = f.event_id
        if (f.event === 'ticket.access_lost') this.subs.delete(f.topic)
        for (const h of this.eventHandlers) h(f)
        break
      case 'counts':
        for (const h of this.countsHandlers) h(f.data)
        break
      case 'resync':
        this.emitResync(f.reason)
        break
      case 'ack':
        if (f.denied?.length) for (const t of f.denied) this.subs.delete(t)
        break
      case 'presence':
        for (const h of this.presenceHandlers) h(f.topic, f.users ?? [])
        break
      case 'pong':
        break
    }
  }
}
