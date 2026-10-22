// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Authentik helpers shared by the SSO specs (sso.spec.ts, smoke-stack.spec.ts):
 * a minimal admin API client (bootstrap token of the dev stack) that creates
 * throwaway IdP users, and the browser steps of an Authentik sign-in.
 */
import type { APIRequestContext, Browser, Page } from '@playwright/test'
import { expect } from './fixtures'

export const AK = process.env.E2E_AUTHENTIK_URL?.replace(/\/$/, '') ?? ''
const AK_TOKEN = process.env.E2E_AUTHENTIK_TOKEN ?? 'dev-akadmin-api-token-0123456789'
/** IdP password of every user created through `authentik(request).createUser`. */
export const IDP_PASSWORD = 'e2e-Sso-password-123!'

export interface AkUser {
  pk: number
  username: string
  email: string
}

/** Minimal Authentik admin API client (v3). */
export function authentik(request: APIRequestContext) {
  const headers = { Authorization: `Bearer ${AK_TOKEN}` }
  const call = async (method: 'GET' | 'POST' | 'PATCH' | 'DELETE', path: string, data?: unknown) => {
    const res = await request.fetch(`${AK}/api/v3${path}`, { method, headers, data })
    expect(res.ok(), `authentik ${method} ${path}: ${res.status()} ${await res.text()}`).toBeTruthy()
    return res.status() === 204 ? null : res.json()
  }
  const groupPk = async (name: string): Promise<string> => {
    const r = await call('GET', `/core/groups/?name=${encodeURIComponent(name)}`)
    expect(r.results, `group ${name}`).toHaveLength(1)
    return r.results[0].pk
  }
  return {
    call,
    groupPk,
    async createUser(slug: string, groups: string[]): Promise<AkUser> {
      const username = `e2e-${slug}-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 5)}`
      const email = `${username}@dev.test`
      const pks = await Promise.all(groups.map(groupPk))
      const u = await call('POST', '/core/users/', { username, name: `E2E ${slug}`, email, is_active: true, type: 'internal', groups: pks, path: 'users' })
      await call('POST', `/core/users/${u.pk}/set_password/`, { password: IDP_PASSWORD })
      return { pk: u.pk, username, email }
    },
    async setGroups(u: AkUser, groups: string[]) {
      await call('PATCH', `/core/users/${u.pk}/`, { groups: await Promise.all(groups.map(groupPk)) })
    },
    async deleteUser(u: AkUser) {
      await call('DELETE', `/core/users/${u.pk}/`)
    },
  }
}

/** Fills Authentik's identification and password stages until the browser leaves the IdP. */
export async function authentikSignIn(page: Page, username: string) {
  await page.waitForURL((u) => u.href.startsWith(AK))
  const uid = page.locator('input[name="uidField"]')
  await uid.fill(username)
  await uid.press('Enter')
  // The password stage replaces the identification stage (same URL, new form).
  await expect(uid).toHaveCount(0)
  const password = page.locator('input[name="password"]:visible')
  await password.fill(IDP_PASSWORD)
  await password.press('Enter')
}

/** Starts SSO from /agent/login and signs in at Authentik. */
export async function ssoLogin(page: Page, username: string) {
  await page.goto('/agent/login')
  await page.getByTestId('sso-button').click()
  await authentikSignIn(page, username)
  // Back in FeatherDesk: either the agent panel (session) or the login page with ?error=.
  await page.waitForURL((u) => !u.href.startsWith(AK) && !u.pathname.includes('/auth/oidc/'), { timeout: 60_000 })
}

/** A new browser context (no IdP or FeatherDesk session carries over) and a page in it. */
export async function freshPage(browser: Browser) {
  const context = await browser.newContext()
  return { context, page: await context.newPage() }
}
