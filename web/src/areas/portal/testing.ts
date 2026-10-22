// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Test helpers for portal / KB / setup view specs: mounts a page under a real
 * route pattern (so `route.params` work) with Vuetify, i18n and Pinia, and
 * reads request bodies from the fetch mock.
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter, RouterView, type RouteLocationNormalized, type RouteRecordRaw, type Router } from 'vue-router'
import { h, type Component } from 'vue'
import { i18n } from '@/i18n'
import { createAppVuetify } from '@/plugins/vuetify'
import { mergeConfig, useConfigStore } from '@/stores/config'
import { useSessionStore } from '@/stores/session'
import type { AuthConfig, Me } from '@/types'
import { loadKbMessages, loadPortalMessages, loadSetupMessages } from './routeGuards'

export const CUSTOMER: Me = {
  id: 'u-cust',
  kind: 'customer',
  role: 'requester',
  name: 'Carla Customer',
  email: 'carla@example.com',
  locale: 'en',
  csrf_token: 'csrf-1',
  has_password: true,
}
export const ORG_MANAGER: Me = { ...CUSTOMER, id: 'u-mgr', role: 'org_manager', name: 'Mario Manager' }

export interface PageOptions {
  path: string
  pattern?: string
  me?: Me | null
  config?: Partial<AuthConfig>
  extraRoutes?: RouteRecordRaw[]
}

const Elsewhere: Component = { name: 'Elsewhere', render: () => h('div', { 'data-testid': 'elsewhere' }) }

export async function mountPage(page: Component, opts: PageOptions) {
  // The routes load these lazy packs in `beforeEnter`; pages mounted directly need them too.
  await Promise.all([loadPortalMessages(), loadKbMessages(), loadSetupMessages()])
  const pinia = createPinia()
  setActivePinia(pinia)
  const config = useConfigStore()
  config.config = mergeConfig({ ...opts.config })
  config.loaded = true
  const session = useSessionStore()
  session.set(opts.me === undefined ? CUSTOMER : opts.me)
  session.checked = true
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: opts.pattern ?? opts.path, component: page }, ...(opts.extraRoutes ?? []), { path: '/:p(.*)*', component: Elsewhere }],
  })
  await router.push(opts.path)
  await router.isReady()
  const Root: Component = { render: () => h(RouterView) }
  const wrapper = mount(Root, { attachTo: document.body, global: { plugins: [pinia, router, createAppVuetify(), i18n] } })
  return { wrapper, router, pinia, config, session }
}

type FetchMock = { mock: { calls: unknown[][] } }

/** Requests seen by the fetch mock: method, path (without /api/v1) and parsed JSON body. */
export async function requests(fn: FetchMock) {
  const out: { method: string; path: string; search: string; body: unknown; headers: Headers }[] = []
  for (const [input, init] of fn.mock.calls as [RequestInfo | URL, RequestInit | undefined][]) {
    const req = input instanceof Request ? input : null
    const url = new URL(req ? req.url : String(input), 'http://localhost')
    const method = (req?.method ?? init?.method ?? 'GET').toUpperCase()
    let body: unknown = undefined
    if (req) {
      const text = await req.clone().text()
      body = text ? safe(text) : undefined
    } else if (typeof init?.body === 'string') body = safe(init.body)
    else if (init?.body) body = init.body
    out.push({ method, path: url.pathname.replace(/^\/api\/v1/, ''), search: url.search, body, headers: req?.headers ?? new Headers(init?.headers) })
  }
  return out
}

function safe(s: string): unknown {
  try {
    return JSON.parse(s)
  } catch {
    return s
  }
}

/** Waits for pending promises and Vue updates. */
export async function settle(n = 4) {
  for (let i = 0; i < n; i++) await new Promise((r) => setTimeout(r, 0))
}

/** `router.resolve()` typed as the normalized location the guards take. */
export function resolveFor(router: Router, path: string): RouteLocationNormalized {
  return router.resolve(path) as unknown as RouteLocationNormalized
}
