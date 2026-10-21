// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Route guards (FE-008). They are UX only — the API enforces every rule
 * (permissions spec I-1). Pure function so it can be unit-tested.
 */
import type { RouteLocationNormalized, RouteLocationRaw } from 'vue-router'
import type { Me, Role } from '@/types'

export type AreaName = 'portal' | 'agent' | 'kb' | 'setup'

declare module 'vue-router' {
  interface RouteMeta {
    area?: AreaName
    /** true = anonymous only (login pages); false/undefined = needs a principal (unless public) */
    guestOnly?: boolean
    public?: boolean
    roles?: Role[]
    /** Also allowed when `GET /me` lists this area in `allowed_areas` (e.g. `project_admin`). */
    orArea?: string
    title?: string
  }
}

export interface GuardState {
  me: Me | null
  setupPending: boolean
  /** support.portal_enabled (default true). False: every portal route is closed. */
  portalEnabled?: boolean
}

/** Same-origin relative path only, to prevent open redirects. */
export function safeRedirect(target: unknown): string | null {
  if (typeof target !== 'string') return null
  if (!target.startsWith('/') || target.startsWith('//') || target.startsWith('/\\')) return null
  return target
}

/** The sign-in page of an area; with the customer portal off, everyone signs in as staff. */
export function loginPathFor(area: AreaName | undefined, portalEnabled = true): string {
  return area === 'agent' || !portalEnabled ? '/agent/login' : '/login'
}

export function homeFor(me: Me | null, portalEnabled = true): string {
  if (!me) return loginPathFor('portal', portalEnabled)
  return me.kind === 'staff' ? '/agent/tickets' : '/tickets'
}

export function decide(to: RouteLocationNormalized, s: GuardState): true | RouteLocationRaw {
  const area = to.meta.area
  const portalEnabled = s.portalEnabled !== false
  if (s.setupPending) return area === 'setup' ? true : { path: '/setup' }
  if (area === 'setup') return { path: homeFor(s.me, portalEnabled) }
  // Customer portal switched off: its routes (login, register, tickets, profile, ...) lead to the staff sign-in.
  // The unknown-URL page keeps rendering; the public KB follows its own `kb_enabled`.
  if (!portalEnabled && area === 'portal' && to.name !== 'not-found') return { path: '/agent/login' }
  if (to.meta.public) return true
  if (to.meta.guestOnly) return s.me ? { path: homeFor(s.me) } : true

  if (!s.me) {
    return { path: loginPathFor(area, portalEnabled), query: { redirect: to.fullPath } }
  }
  if (area === 'agent' && s.me.kind !== 'staff') return { path: '/tickets' }
  if (area === 'portal' && s.me.kind !== 'customer') return { path: '/agent/tickets' }
  if (to.meta.roles && !to.meta.roles.includes(s.me.role) && !(to.meta.orArea && s.me.allowed_areas?.includes(to.meta.orArea))) return { path: homeFor(s.me) }
  return true
}
