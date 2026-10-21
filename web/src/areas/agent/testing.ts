// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Test harness for agent-panel views (Vitest only; never imported by app code).
 *
 *   const h = await mountAgent(TicketDetailPage, {
 *     path: '/agent/tickets/:number', route: '/agent/tickets/1042',
 *     me: fx.me('agent', 1),
 *     api: { 'GET /agent/tickets/by-number/1042': () => json(fx.ticket()) },
 *   })
 *   h.calls('PATCH /agent/tickets/t-1')[0].body
 *   h.socket().push({ type: 'counts', data: {...} })
 */
import type { WsServerFrame } from '@/realtime/client'
import { mount, type ComponentMountingOptions } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory, type Router } from 'vue-router'
import { defineComponent, h as hFn, type Component } from 'vue'
import { i18n } from '@/i18n'
import { createAppVuetify } from '@/plugins/vuetify'
import { mockFetch, json, flush } from '@/test/mount'
import { useSessionStore } from '@/stores/session'
import { useConfigStore, mergeConfig } from '@/stores/config'
import { useRealtimeStore } from '@/stores/realtime'
import type { WebSocketLike } from '@/realtime/client'
import type { AuthConfig, Me } from '@/types'
import type { components } from '@/api/schema'
import enAgent from '@/locales/agent/en.json'
import enPrivacy from '@/locales/privacy/en.json'

export { json, flush }

type S = components['schemas']

let agentMessagesLoaded = false
/** Merges the agent pack into `en` synchronously (the app loads it lazily per route). */
export function loadAgentMessagesForTests() {
  if (agentMessagesLoaded) return
  i18n.global.mergeLocaleMessage('en', enAgent as never)
  i18n.global.mergeLocaleMessage('en', enPrivacy as never)
  agentMessagesLoaded = true
}

/** A WebSocket stand-in: records sent frames, lets the test push server frames. */
export class MockSocket implements WebSocketLike {
  readyState = 0
  sent: Record<string, unknown>[] = []
  onopen: ((ev: unknown) => void) | null = null
  onclose: ((ev: { code: number }) => void) | null = null
  onmessage: ((ev: { data: unknown }) => void) | null = null
  onerror: ((ev: unknown) => void) | null = null
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
  /** A server frame as the generated `WsServerFrame` defines it (CON-013): a fixture that drifts from the spec fails the typecheck. */
  push(frame: WsServerFrame) {
    this.onmessage?.({ data: JSON.stringify(frame) })
  }
  frames(op?: string) {
    return op ? this.sent.filter((f) => f.op === op) : this.sent
  }
}

export type Handler = (url: URL, init: Omit<RequestInit, 'body'> & { body?: any }) => Response | Promise<Response>

export interface MountAgentOptions<T extends Component> {
  /** Route pattern the component is mounted under (gives `useRoute().params`). */
  path?: string
  /** The concrete URL to navigate to before mounting. */
  route?: string
  me?: Partial<Me> | null
  config?: Partial<AuthConfig>
  /**
   * `'METHOD /api-path'` → handler. The path excludes `/api/v1` and the query
   * string; unmatched requests return 404 so missing mocks are visible.
   */
  api?: Record<string, Handler | object | Response>
  props?: ComponentMountingOptions<T>['props']
  /** Extra routes (e.g. navigation targets) */
  routes?: { path: string; name?: string }[]
  /** Open the realtime socket immediately (default true). */
  connect?: boolean
}

export interface AgentHarness {
  wrapper: ReturnType<typeof mount>
  router: Router
  /** Every request so far; filter with `'METHOD /path'` (exact) or a RegExp on `'METHOD /path?query'`. */
  calls: (match?: string | RegExp) => { method: string; path: string; url: URL; body?: unknown; headers: Headers }[]
  socket: () => MockSocket
  flush: () => Promise<void>
}

const Stub = defineComponent({ render: () => hFn('div') })

export async function mountAgent<T extends Component>(component: T, opts: MountAgentOptions<T> = {}): Promise<AgentHarness> {
  loadAgentMessagesForTests()
  const pinia = createPinia()
  setActivePinia(pinia)

  const config = useConfigStore()
  config.config = mergeConfig({ ...(opts.config ?? {}) })
  config.loaded = true
  const session = useSessionStore()
  session.set(opts.me === null ? null : (fx.me('agent', 1, opts.me ?? {}) as Me))
  session.checked = true

  const sockets: MockSocket[] = []
  const rt = useRealtimeStore()
  rt.start({
    createSocket: () => {
      const s = new MockSocket()
      sockets.push(s)
      if (opts.connect !== false) queueMicrotask(() => s.open())
      return s
    },
    setTimer: () => 0,
    clearTimer: () => {},
  })

  const handlers = opts.api ?? {}
  const { calls } = mockFetch(async (raw, init) => {
    const url = new URL(raw, 'http://localhost')
    const path = url.pathname.replace(/^\/api\/v1/, '')
    const method = (init.method ?? 'GET').toUpperCase()
    const h = handlers[`${method} ${path}`]
    if (h === undefined) return json({ title: `unmocked ${method} ${path}`, status: 404 }, 404)
    if (typeof h === 'function') {
      let body: unknown = init.body
      if (typeof body === 'string') {
        try {
          body = JSON.parse(body)
        } catch {
          /* keep raw */
        }
      }
      return (h as Handler)(url, { ...init, body })
    }
    return h instanceof Response ? h : json(h)
  })

  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      ...(opts.path ? [{ path: opts.path, component: Stub }] : []),
      ...(opts.routes ?? []).map((r) => ({ ...r, component: Stub })),
      { path: '/:p(.*)*', component: Stub },
    ],
  })
  await router.push(opts.route ?? opts.path ?? '/agent')
  await router.isReady()

  const vuetify = createAppVuetify()
  const wrapper = mount(component as Component, {
    attachTo: document.body,
    props: opts.props as Record<string, unknown>,
    global: { plugins: [pinia, router, vuetify, i18n] },
  })
  await flush()
  await flush()

  return {
    wrapper,
    router,
    calls: (match) =>
      calls
        .map((c) => {
          const url = new URL(c.url, 'http://localhost')
          const path = url.pathname.replace(/^\/api\/v1/, '')
          return { method: c.method, path, url, body: c.body, headers: new Headers(c.init.headers) }
        })
        .filter((c) =>
          match === undefined
            ? true
            : typeof match === 'string'
              ? `${c.method} ${c.path}` === match
              : match.test(`${c.method} ${c.path}${c.url.search}`),
        ),
    socket: () => sockets[sockets.length - 1]!,
    flush: async () => {
      await flush()
      await flush()
    },
  }
}

/** Finds an element by `data-testid` (also inside teleported overlays). */
export function byTestId(id: string): HTMLElement | null {
  return document.body.querySelector(`[data-testid="${id}"]`)
}
export function allByTestId(id: string): HTMLElement[] {
  return [...document.body.querySelectorAll<HTMLElement>(`[data-testid="${id}"]`)]
}

/** Sets an input's value and fires `input` (works inside teleported dialogs). */
export async function typeInto(el: Element | null, value: string) {
  const input = (el?.matches('input,textarea') ? el : el?.querySelector('input,textarea')) as HTMLInputElement | null
  if (!input) throw new Error('typeInto: no input')
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
  await flush()
}

export async function click(el: Element | null) {
  if (!el) throw new Error('click: element not found')
  ;(el as HTMLElement).click()
  await flush()
  await flush()
}

const ISO = '2026-10-20T10:00:00Z'

/** Fixtures shaped like the OpenAPI schemas. */
export const fx = {
  me(role: 'agent' | 'supervisor' | 'admin' = 'agent', tier = 1, extra: Partial<Me> = {}): Me {
    return {
      id: 'u-me',
      kind: 'staff',
      role,
      tier: role === 'agent' ? tier : 3,
      name: 'Alice Agent',
      email: 'alice@example.com',
      csrf_token: 'csrf-1',
      locale: 'en',
      teams: ['team-1'],
      ...extra,
    }
  },
  ticket(extra: Partial<S['Ticket']> = {}): S['Ticket'] {
    return {
      id: 't-1',
      number: 1042,
      ref: `WEB-${extra.number ?? 1042}`,
      project_id: 'p-web',
      project: { id: 'p-web', key: 'WEB', name: 'Web App', color: '#1976d2' },
      subject: 'Printer on fire',
      status: 'open',
      priority: 'normal',
      tier: 1,
      team_id: 'team-1',
      category_id: null,
      assignee: null,
      requester: { id: 'c-1', name: 'Ada Customer', kind: 'customer' },
      organization_id: null,
      channel: 'portal',
      tags: [],
      sla_breached: false,
      custom_fields: {},
      created_at: ISO,
      updated_at: ISO,
      messages: [
        { id: 'm-1', ticket_id: 't-1', kind: 'public', body_md: 'It is on fire.', author: { id: 'c-1', name: 'Ada Customer', kind: 'customer' }, created_at: ISO, attachments: [] },
      ],
      attachments: [],
      watchers: [],
      links: [],
      allowed_actions: ['reply', 'note', 'set_status', 'set_priority', 'set_category', 'assign', 'transfer', 'escalate', 'edit_fields', 'watch', 'link', 'split'],
      ...extra,
    }
  },
  summary(extra: Partial<S['TicketSummary']> = {}): S['TicketSummary'] {
    return {
      id: 't-1',
      number: 1042,
      ref: `WEB-${extra.number ?? 1042}`,
      project_id: 'p-web',
      project: { id: 'p-web', key: 'WEB', name: 'Web App', color: '#1976d2' },
      subject: 'Printer on fire',
      status: 'open',
      priority: 'normal',
      tier: 1,
      team_id: 'team-1',
      assignee: null,
      requester: { id: 'c-1', name: 'Ada Customer', kind: 'customer' },
      channel: 'portal',
      created_at: ISO,
      updated_at: ISO,
      ...extra,
    }
  },
  team(extra: Partial<S['Team']> = {}): S['Team'] {
    return { id: 'team-1', name: 'Support', notify_mode: 'agents', created_at: ISO, updated_at: ISO, ...extra }
  },
  category(extra: Partial<S['Category']> = {}): S['Category'] {
    return { id: 'cat-1', name: 'Billing', active: true, sort: 1, project_id: 'p-web', ...extra }
  },
  staffRef(extra: Partial<S['StaffRef']> = {}): S['StaffRef'] {
    return { id: 'u-2', name: 'Bob Builder', role: 'agent', tier: 2, team_ids: ['team-1'], ...extra }
  },
  user(extra: Partial<S['User']> = {}): S['User'] {
    return {
      id: 'c-1',
      kind: 'customer',
      role: 'requester',
      name: 'Ada Customer',
      email: 'ada@example.com',
      locale: 'en',
      status: 'active',
      created_at: ISO,
      updated_at: ISO,
      ...extra,
    }
  },
  project(extra: Partial<S['AgentProject']> = {}): S['AgentProject'] {
    return {
      id: 'p-web', key: 'WEB', name: 'Web App', description: '', color: '#1976d2', icon: '', active: true, sort: 0,
      portal_visible: true, portal_title: null, portal_intro_md: null, logo_url: null, logo_asset_id: null, primary_color: null,
      default_team_id: null, team_ids: ['team-1'], created_at: ISO, updated_at: ISO, my_role: 'member', can_admin: false, ...extra,
    }
  },
  page<T>(items: T[], next_cursor: string | null = null) {
    return { items, next_cursor }
  },
  iso: ISO,
}
