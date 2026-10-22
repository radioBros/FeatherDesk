// SPDX-License-Identifier: AGPL-3.0-only

import { decide, safeRedirect, homeFor, loginPathFor } from './guards'
import type { RouteLocationNormalized } from 'vue-router'
import type { Me } from '@/types'

const route = (fullPath: string, meta: RouteLocationNormalized['meta']) =>
  ({ fullPath, path: fullPath.split('?')[0], meta } as unknown as RouteLocationNormalized)

const staff: Me = { id: 's', kind: 'staff', role: 'agent', tier: 1, name: 'S', email: 's@x.io', csrf_token: 't' }
const admin: Me = { ...staff, role: 'admin' }
const customer: Me = { id: 'c', kind: 'customer', role: 'requester', name: 'C', email: 'c@x.io', csrf_token: 't' }

describe('route guards (FE-008)', () => {
  it('customer opening an agent page lands on /tickets', () => {
    expect(decide(route('/agent/tickets', { area: 'agent' }), { me: customer, setupPending: false })).toEqual({ path: '/tickets' })
  })
  it('staff opening a portal page lands on /agent/tickets', () => {
    expect(decide(route('/tickets', { area: 'portal' }), { me: staff, setupPending: false })).toEqual({ path: '/agent/tickets' })
  })
  it('agent opening an admin page is sent to their home', () => {
    expect(decide(route('/agent/admin/staff', { area: 'agent', roles: ['admin'] }), { me: staff, setupPending: false })).toEqual({ path: '/agent/tickets' })
    expect(decide(route('/agent/admin/staff', { area: 'agent', roles: ['admin'] }), { me: admin, setupPending: false })).toBe(true)
  })
  it('anonymous goes to the area login with a redirect', () => {
    expect(decide(route('/tickets/123', { area: 'portal' }), { me: null, setupPending: false })).toEqual({ path: '/login', query: { redirect: '/tickets/123' } })
    expect(decide(route('/agent/tickets/5', { area: 'agent' }), { me: null, setupPending: false })).toEqual({ path: '/agent/login', query: { redirect: '/agent/tickets/5' } })
  })
  it('guest-only pages bounce signed-in users home; public pages always pass', () => {
    expect(decide(route('/login', { area: 'portal', guestOnly: true }), { me: customer, setupPending: false })).toEqual({ path: '/tickets' })
    expect(decide(route('/kb', { area: 'kb', public: true }), { me: null, setupPending: false })).toBe(true)
  })
  it('setup pending forces /setup, and /setup is closed afterwards', () => {
    expect(decide(route('/tickets', { area: 'portal' }), { me: null, setupPending: true })).toEqual({ path: '/setup' })
    expect(decide(route('/setup', { area: 'setup', public: true }), { me: null, setupPending: true })).toBe(true)
    expect(decide(route('/setup', { area: 'setup', public: true }), { me: null, setupPending: false })).toEqual({ path: '/login' })
  })
  it('safeRedirect only allows same-origin relative paths', () => {
    expect(safeRedirect('/tickets/1')).toBe('/tickets/1')
    expect(safeRedirect('//evil.com')).toBeNull()
    expect(safeRedirect('https://evil.com')).toBeNull()
    expect(safeRedirect('/\\evil.com')).toBeNull()
    expect(safeRedirect(undefined)).toBeNull()
  })
  it('homeFor', () => {
    expect(homeFor(null)).toBe('/login')
    expect(homeFor(staff)).toBe('/agent/tickets')
    expect(homeFor(customer)).toBe('/tickets')
  })
})

describe('customer portal switched off (support.portal_enabled=false)', () => {
  const off = { setupPending: false, portalEnabled: false }
  it('every portal route leads to the staff sign-in, signed in or not', () => {
    for (const meta of [
      { area: 'portal' as const, guestOnly: true },
      { area: 'portal' as const },
      { area: 'portal' as const, public: true },
      { area: 'portal' as const, roles: ['org_manager' as const] },
    ]) {
      expect(decide(route('/login', meta), { ...off, me: null })).toEqual({ path: '/agent/login' })
      expect(decide(route('/tickets', meta), { ...off, me: staff })).toEqual({ path: '/agent/login' })
    }
  })
  it('the unknown-URL page, the KB and the agent area are untouched', () => {
    expect(decide({ ...route('/nope', { area: 'portal', public: true }), name: 'not-found' } as RouteLocationNormalized, { ...off, me: null })).toBe(true)
    expect(decide(route('/kb', { area: 'kb', public: true }), { ...off, me: null })).toBe(true)
    expect(decide(route('/agent/tickets', { area: 'agent' }), { ...off, me: null })).toEqual({ path: '/agent/login', query: { redirect: '/agent/tickets' } })
    expect(decide(route('/agent/tickets', { area: 'agent' }), { ...off, me: staff })).toBe(true)
    expect(decide(route('/agent/login', { area: 'agent', guestOnly: true }), { ...off, me: staff })).toEqual({ path: '/agent/tickets' })
  })
  it('the portal stays open when the flag is on or missing', () => {
    expect(decide(route('/login', { area: 'portal', guestOnly: true }), { me: null, setupPending: false })).toBe(true)
    expect(decide(route('/login', { area: 'portal', guestOnly: true }), { me: null, setupPending: false, portalEnabled: true })).toBe(true)
  })
  it('home and login paths follow the flag', () => {
    expect(homeFor(null, false)).toBe('/agent/login')
    expect(loginPathFor('portal', false)).toBe('/agent/login')
    expect(loginPathFor('portal', true)).toBe('/login')
    expect(loginPathFor('agent', true)).toBe('/agent/login')
  })
})
