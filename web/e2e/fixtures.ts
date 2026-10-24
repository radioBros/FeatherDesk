// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Shared E2E helpers: sign in through the real API (cookies land in the page's
 * browser context), seed data with a personal access token, and collect
 * console/CSP problems.
 */
import { test as base, expect, type APIRequestContext, type Frame, type Locator, type Page } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'

export const ADMIN = {
  email: process.env.E2E_ADMIN_EMAIL ?? 'admin@e2e.test',
  password: process.env.E2E_ADMIN_PASSWORD ?? 'e2e-admin-password-123',
}

export interface Credentials {
  email: string
  password: string
}

/** Signs in via POST /api/v1/auth/login; the session cookie is shared with `page`. */
export async function loginAs(page: Page, audience: 'staff' | 'customer', creds: Credentials) {
  const res = await page.request.post('/api/v1/auth/login', { data: { audience, ...creds } })
  expect(res.ok(), `login ${creds.email}: ${res.status()} ${await res.text()}`).toBeTruthy()
  const me = await (await page.request.get('/api/v1/me')).json()
  return me as { id: string; csrf_token: string; kind: string }
}

/** Creates a PAT for the signed-in user (staff) and returns an API client using it. */
export async function tokenClient(page: Page, scopes: string[] = ['tickets:read', 'tickets:write', 'directory:read', 'directory:write']) {
  const meRes = await page.request.get('/api/v1/me')
  const me = await meRes.json()
  // Cookie-authenticated mutations need the CSRF token AND a same-origin Origin
  // header (server CSRF + Origin check); `page.request` sends neither by itself.
  const res = await page.request.post('/api/v1/me/tokens', {
    data: { name: `e2e-${Date.now()}`, scopes },
    headers: { 'X-CSRF-Token': me.csrf_token, Origin: new URL(meRes.url()).origin },
  })
  expect(res.ok(), await res.text()).toBeTruthy()
  const { token } = (await res.json()) as { token: string }
  const headers = { Authorization: `Bearer ${token}` }
  const req: APIRequestContext = page.request
  return {
    token,
    get: (path: string) => req.get('/api/v1' + path, { headers }),
    post: (path: string, data: unknown) => req.post('/api/v1' + path, { headers, data }),
    patch: (path: string, data: unknown) => req.patch('/api/v1' + path, { headers, data }),
  }
}

/** Fails the test on console errors or CSP violations (PLT-007 relies on this). */
export function watchConsole(page: Page) {
  const problems: string[] = []
  page.on('console', (msg) => {
    const text = msg.text()
    if (msg.type() === 'error' || /Content Security Policy|Refused to (load|execute|apply)/i.test(text)) {
      // A 401 from /me for anonymous visitors is expected.
      if (/401 \(Unauthorized\)/.test(text)) return
      problems.push(`${msg.type()}: ${text}`)
    }
  })
  page.on('pageerror', (err) => problems.push(`pageerror: ${err.message}`))
  return problems
}

const AXE_TAGS = ['wcag2a', 'wcag2aa']

/** A frame whose document runs no scripts: its iframe element's selector chain from the top document. */
interface ScriptlessFrame {
  chain: string[]
  frame: Frame
}

/** A selector that matches exactly this element in its own document (id, else an nth-of-type path). */
function uniqueSelector(el: Element): string {
  if (el.id && el.ownerDocument.querySelectorAll(`#${CSS.escape(el.id)}`).length === 1) return `#${CSS.escape(el.id)}`
  const parts: string[] = []
  for (let e: Element | null = el; e && e !== e.ownerDocument.documentElement; e = e.parentElement) {
    const same = e.parentElement ? [...e.parentElement.children].filter((c) => c.localName === e!.localName) : [e]
    parts.unshift(`${e.localName}:nth-of-type(${same.indexOf(e) + 1})`)
  }
  return ['html', ...parts].join(' > ')
}

/**
 * The frames whose documents cannot run scripts: `<iframe sandbox>` without
 * allow-scripts, or a `Content-Security-Policy: sandbox` response (the email
 * previews have both, FE-047). Playwright still evaluates there, but timers
 * and animation frames never fire, so axe.runPartial (which waits on
 * setTimeout) never settles and @axe-core/playwright, which awaits it with no
 * timeout, hangs forever. Detected by behaviour, not by attribute, so the
 * header case is caught too.
 */
async function scriptlessFrames(page: Page): Promise<ScriptlessFrame[]> {
  const out: ScriptlessFrame[] = []
  const chains = new Map<Frame, string[]>([[page.mainFrame(), []]])
  const walk = async (parent: Frame) => {
    for (const frame of parent.childFrames()) {
      const el = await frame.frameElement().catch(() => null)
      if (!el) continue
      const chain = [...chains.get(parent)!, await el.evaluate(uniqueSelector)]
      const timersRun = await Promise.race([
        frame.evaluate(() => new Promise<boolean>((resolve) => setTimeout(() => resolve(true), 0))).catch(() => false),
        new Promise<boolean>((resolve) => setTimeout(() => resolve(false), 1_000)),
      ])
      if (timersRun) {
        chains.set(frame, chain)
        await walk(frame)
      } else out.push({ chain, frame })
    }
  }
  await walk(page.mainFrame())
  return out
}

/**
 * Axe accessibility scan; returns serious/critical violations only.
 *
 * Frames that run no scripts cannot host axe (see scriptlessFrames), so they
 * are scanned in two other ways instead of hanging the run: the iframe
 * element itself (frame-title, …) by a pass that does not descend into
 * frames, and the frame's document as a page of its own.
 */
export async function a11y(page: Page) {
  const scriptless = await scriptlessFrames(page)
  const main = new AxeBuilder({ page }).withTags(AXE_TAGS)
  for (const { chain } of scriptless) main.exclude(chain.length === 1 ? chain[0]! : chain)
  const violations = (await main.analyze()).violations

  const topLevel = scriptless.filter(({ chain }) => chain.length === 1)
  if (topLevel.length) {
    // Legacy mode runs axe.run in the top document only; iframes:false keeps it out of the frames.
    const own = new AxeBuilder({ page }).withTags(AXE_TAGS).setLegacyMode().options({ iframes: false })
    for (const { chain } of topLevel) own.include(chain[0]!)
    violations.push(...(await own.analyze()).violations)
  }

  for (const { frame } of scriptless) {
    // The frame's DOM in a fresh page of the same context (same viewport), where scripts run.
    const html = await frame.content()
    const doc = await page.context().newPage()
    try {
      await doc.setContent(html, { waitUntil: 'load' })
      violations.push(...(await new AxeBuilder({ page: doc }).withTags(AXE_TAGS).analyze()).violations.map((v) => ({ ...v, id: `${v.id} (in frame ${frame.url()})` })))
    } finally {
      await doc.close()
    }
  }
  return violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')
}

let seq = 0
let extra = 0
/**
 * A fresh client IP for a browser context a test opens itself
 * (`browser.newContext()` does not get the fixture's header): 198.19.0.0/16,
 * the other half of the benchmark range, so it never collides with the
 * per-test fixture addresses.
 */
export function clientIp(workerIndex?: number): Record<string, string> {
  let w = workerIndex ?? 0
  if (workerIndex === undefined) {
    try {
      w = test.info().workerIndex
    } catch {
      /* outside a running test (e.g. a script reusing the helpers): worker 0 */
    }
  }
  const n = (w * 4096 + ++extra) % 65_536
  return { 'X-Forwarded-For': `198.19.${n >> 8}.${n & 255}` }
}
/**
 * Every test is its own client: a distinct `X-Forwarded-For` (198.18.0.0/15,
 * benchmark range) in every context it opens, honoured because the E2E server
 * trusts loopback as a proxy (FD_TRUSTED_PROXIES). The per-IP login throttle
 * (AUTH-012, 10/min) then applies per test as it would per real user, instead
 * of to the whole suite sharing 127.0.0.1. Ignored by servers that do not trust
 * the hop (E2E_BASE_URL deployments).
 */
/** Fills a MarkdownEditor (WYSIWYG or source) identified by its field test id wrapper. */
export async function fillEditor(locator: Locator, text: string) {
  // The WYSIWYG surface loads lazily; type into it (not the Markdown source view), as a person would.
  const editable = locator.locator('[contenteditable="true"]')
  await editable.waitFor()
  await editable.fill(text)
}

export const test = base.extend({
  extraHTTPHeaders: async ({ extraHTTPHeaders }, use, info) => {
    const n = (info.workerIndex * 2048 + ++seq) % 65_536
    await use({ ...extraHTTPHeaders, 'X-Forwarded-For': `198.18.${n >> 8}.${n & 255}` })
  },
})
export { expect }
