// SPDX-License-Identifier: AGPL-3.0-only

/**
 * PLT-007: the Content-Security-Policy the server sends is strict, and the real
 * SPA (portal, agent and admin pages) runs under it with zero violations.
 * Violations are caught twice: the `securitypolicyviolation` DOM event
 * (recorded by an init script before any app code runs) and the console.
 * Attachments are served as sandboxed downloads. The second block holds the
 * browser-side cases of the AUTH-020 security suite.
 */
import type { BrowserContext, Page } from '@playwright/test'
import { test, expect, ADMIN } from './fixtures'
import { apiFor, signIn, uid } from './agent/helpers'

const RECORDER = `
  window.__cspViolations = [];
  document.addEventListener('securitypolicyviolation', (e) => {
    window.__cspViolations.push(e.violatedDirective + ' blocked=' + e.blockedURI + ' at ' + (e.sourceFile || '') + ':' + e.lineNumber);
  });
`

async function record(context: BrowserContext, page: Page) {
  await context.addInitScript(RECORDER)
  const consoleHits: string[] = []
  page.on('console', (msg) => {
    if (/Content Security Policy|Refused to (load|execute|apply|connect|frame)/i.test(msg.text())) consoleHits.push(msg.text())
  })
  return {
    async check(path: string) {
      await page.goto(path)
      await page.waitForLoadState('networkidle')
      const dom = (await page.evaluate(() => (window as unknown as { __cspViolations?: string[] }).__cspViolations ?? [])) as string[]
      expect(dom, `CSP violations (DOM event) on ${path}`).toEqual([])
      expect(consoleHits, `CSP violations (console) on ${path}`).toEqual([])
    },
  }
}

function directives(csp: string) {
  const out: Record<string, string[]> = {}
  for (const part of csp.split(';')) {
    const [name, ...values] = part.trim().split(/\s+/)
    if (name) out[name] = values
  }
  return out
}

test.describe('content security policy (PLT-007)', () => {
  test('SPA and API responses carry the strict policy and security headers', async ({ page, baseURL }) => {
    for (const path of ['/', '/login', '/agent/login', '/api/v1/auth/config']) {
      const res = await page.request.get(path)
      const h = res.headers()
      const csp = h['content-security-policy'] ?? ''
      const d = directives(csp)
      expect(d['script-src'], `${path}: ${csp}`).toEqual(["'self'"])
      expect(d['default-src']).toEqual(["'self'"])
      expect(d['frame-ancestors']).toEqual(["'none'"])
      expect(d['object-src']).toEqual(["'none'"])
      expect(d['base-uri']).toEqual(["'self'"])
      expect(d['form-action']?.[0]).toBe("'self'")
      // WebSocket origin listed explicitly: older engines do not match ws(s) with 'self'.
      const wsOrigin = new URL(baseURL!).origin.replace(/^http/, 'ws')
      expect(d['connect-src']).toContain(wsOrigin)
      expect(csp).not.toContain('unsafe-eval')
      expect(h['x-content-type-options']).toBe('nosniff')
      expect(h['referrer-policy']).toBe('strict-origin-when-cross-origin')
      expect(h['cross-origin-opener-policy']).toBe('same-origin')
      expect(h['permissions-policy']).toBeTruthy()
    }
  })

  test('public pages run with zero CSP violations', async ({ page, context }) => {
    const r = await record(context, page)
    for (const path of ['/', '/login', '/register', '/forgot-password', '/kb', '/agent/login']) {
      await r.check(path)
    }
  })

  test('agent and admin pages run with zero CSP violations', async ({ browser }) => {
    const admin = await signIn(browser, 'staff', ADMIN)
    const api = await apiFor(admin.page)
    const id = uid()
    const ticket = await api.post(
      '/agent/tickets',
      { subject: `CSP check ${id}`, body_md: 'Rendered **markdown** with a [link](https://example.com).', requester_email: `csp-${id}@e2e.test`, requester_name: 'CSP Check' },
      { 'Idempotency-Key': `csp-${id}` },
    )
    const r = await record(admin.context, admin.page)
    for (const path of [
      '/agent/tickets',
      `/agent/tickets/${ticket.number}`,
      '/agent/tickets/new',
      '/agent/customers',
      '/agent/kb',
      '/agent/reports',
      '/agent/profile',
      '/agent/admin/settings',
      '/agent/admin/branding',
      '/agent/admin/email',
      '/agent/admin/system',
      '/agent/admin/queue',
      '/agent/admin/webhooks',
    ]) {
      await r.check(path)
    }
    await admin.context.close()
  })

  test('attachments are sandboxed downloads unless a safe image', async ({ browser }) => {
    const admin = await signIn(browser, 'staff', ADMIN)
    const api = await apiFor(admin.page)
    const origin = new URL((await admin.page.request.get('/api/v1/me')).url()).origin
    const headers = { 'X-CSRF-Token': api.me.csrf_token as string, Origin: origin }
    const upload = async (name: string, mimeType: string, body: Buffer) => {
      const res = await admin.page.request.post('/api/v1/agent/attachments', { headers, multipart: { file: { name, mimeType, buffer: body } } })
      expect(res.status(), await res.text()).toBe(201)
      return (await res.json()) as { id: string }
    }
    const html = await upload('evil.html', 'text/html', Buffer.from('<!doctype html><script>alert(document.cookie)</script>'))
    let res = await admin.page.request.get(`/api/v1/agent/attachments/${html.id}`)
    expect(res.status()).toBe(200)
    expect(res.headers()['content-disposition']).toMatch(/^attachment/)
    expect(res.headers()['x-content-type-options']).toBe('nosniff')
    expect(res.headers()['content-security-policy']).toBe('sandbox')

    // 1×1 transparent PNG: inline, still sandboxed and nosniff.
    const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGBgAAAABQABh6FO1AAAAABJRU5ErkJggg==', 'base64')
    const img = await upload('pixel.png', 'image/png', png)
    res = await admin.page.request.get(`/api/v1/agent/attachments/${img.id}`)
    expect(res.status()).toBe(200)
    expect(res.headers()['content-disposition']).toMatch(/^inline/)
    expect(res.headers()['x-content-type-options']).toBe('nosniff')
    expect(res.headers()['content-security-policy']).toBe('sandbox')
    await admin.context.close()
  })
})

/**
 * AUTH-020 browser part (the Go suite in internal/authn covers the server
 * side; scripts/proxy-test.sh covers cookie flags behind real HTTPS proxies).
 */
test.describe('auth security in the browser (AUTH-020)', () => {
  test('session cookie is HttpOnly and SameSite, auth responses are no-store', async ({ page }) => {
    const res = await page.request.post('/api/v1/auth/login', { data: { audience: 'staff', ...ADMIN } })
    expect(res.status()).toBe(200)
    expect(res.headers()['cache-control']).toContain('no-store')
    const cookies = await page.context().cookies()
    const session = cookies.find((c) => c.name === 'fd_session')
    expect(session, JSON.stringify(cookies.map((c) => c.name))).toBeTruthy()
    expect(session!.httpOnly).toBe(true)
    expect(['Lax', 'Strict']).toContain(session!.sameSite)
    await page.goto('/agent/tickets')
    expect(await page.evaluate(() => document.cookie)).not.toContain('fd_session')
    const me = await page.request.get('/api/v1/me')
    expect(me.status()).toBe(200)
    expect(me.headers()['cache-control']).toContain('no-store')
  })

  test('login never redirects off-site', async ({ page, baseURL }) => {
    for (const evil of ['https://evil.example/x', '//evil.example/x', '/\\evil.example', 'javascript:alert(1)']) {
      await page.context().clearCookies()
      await page.goto(`/agent/login?redirect=${encodeURIComponent(evil)}`)
      await page.getByTestId('login-email').locator('input').fill(ADMIN.email)
      await page.getByTestId('login-password').locator('input').fill(ADMIN.password)
      await page.getByTestId('login-submit').click()
      await page.waitForURL((u) => !u.pathname.startsWith('/agent/login'))
      expect(new URL(page.url()).origin, evil).toBe(new URL(baseURL!).origin)
    }
  })

  test('a PAT in the query string and a session cookie used as bearer are ignored', async ({ page }) => {
    await page.request.post('/api/v1/auth/login', { data: { audience: 'staff', ...ADMIN } })
    const session = (await page.context().cookies()).find((c) => c.name === 'fd_session')!.value
    const me = await (await page.request.get('/api/v1/me')).json()
    const origin = new URL((await page.request.get('/api/v1/me')).url()).origin
    const tok = await page.request.post('/api/v1/me/tokens', {
      data: { name: `auth020-${Date.now()}`, scopes: ['tickets:read'] },
      headers: { 'X-CSRF-Token': me.csrf_token, Origin: origin },
    })
    expect(tok.status(), await tok.text()).toBe(201)
    const { token } = (await tok.json()) as { token: string }
    await page.context().clearCookies()
    for (const q of [`access_token=${token}`, `token=${token}`, `api_key=${token}`]) {
      expect((await page.request.get(`/api/v1/me?${q}`)).status(), q).toBe(401)
    }
    expect((await page.request.get('/api/v1/me', { headers: { Authorization: `Bearer ${session}` } })).status()).toBe(401)
    expect((await page.request.get('/api/v1/me', { headers: { Authorization: `Bearer ${token}` } })).status()).toBe(200)
  })
})
