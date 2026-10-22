// SPDX-License-Identifier: AGPL-3.0-only

import { mount, type ComponentMountingOptions } from '@vue/test-utils'
import { createPinia, setActivePinia, type Pinia } from 'pinia'
import { createRouter, createMemoryHistory, type Router } from 'vue-router'
import type { Component } from 'vue'
import { i18n } from '@/i18n'
import { createAppVuetify } from '@/plugins/vuetify'

export interface Harness {
  pinia: Pinia
  router: Router
}

/** Mounts a component with the real Vuetify, i18n, Pinia and a memory router. */
export function mountApp<T extends Component>(
  component: T,
  options: ComponentMountingOptions<T> & { route?: string } = {},
) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }],
  })
  void router.push(options.route ?? '/')
  const vuetify = createAppVuetify()
  const wrapper = mount(component, {
    attachTo: document.body,
    ...options,
    global: { ...(options.global ?? {}), plugins: [pinia, router, vuetify, i18n, ...(options.global?.plugins ?? [])] },
  } as ComponentMountingOptions<T>)
  return { wrapper, pinia, router }
}

/**
 * DataTable asks for the next page when its body is scrolled to the end. jsdom
 * has no layout: give the body a size for one scroll event, then take it away
 * (a body that stays "at the end" would page through a mock forever).
 */
export function scrollTableToEnd(root: Element) {
  const body = root.querySelector<HTMLElement>('.v-table__wrapper')
  if (!body) throw new Error('no DataTable body')
  Object.defineProperty(body, 'clientHeight', { configurable: true, value: 400 })
  Object.defineProperty(body, 'scrollHeight', { configurable: true, value: 400 })
  body.dispatchEvent(new Event('scroll'))
  delete (body as { clientHeight?: number }).clientHeight
  delete (body as { scrollHeight?: number }).scrollHeight
}

export function flush() {
  return new Promise((r) => setTimeout(r, 0))
}

/** A fetch mock recording calls; responses default to 200 {}. */
export function mockFetch(handler: (url: string, init: RequestInit) => Response | Promise<Response> = () => json({})) {
  const calls: { url: string; init: RequestInit; method: string; body?: unknown }[] = []
  const fn = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
    const req = input instanceof Request ? input : null
    const merged: RequestInit = req ? { method: req.method, headers: req.headers, ...init } : init
    // Typed-client calls pass a Request: read its body so specs can assert on it.
    let raw: unknown = init.body
    if (req && raw === undefined && req.body) raw = await req.clone().text()
    let body: unknown = raw
    if (typeof raw === 'string' && raw) {
      try {
        body = JSON.parse(raw)
      } catch {
        body = raw
      }
    }
    if (req && typeof raw === 'string' && merged.body === undefined) merged.body = raw
    const method = (merged.method ?? 'GET').toUpperCase()
    calls.push({ url, init: merged, method, body })
    return handler(url, merged)
  })
  vi.stubGlobal('fetch', fn)
  return { fn, calls }
}

export function json(body: unknown, status = 200, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': status >= 400 ? 'application/problem+json' : 'application/json', ...headers },
  })
}
