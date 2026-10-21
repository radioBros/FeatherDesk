// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Portal E2E helpers: unique identities per run/project and Mailpit access.
 *
 * Mailpit is part of the dev compose (DEP-004). Set E2E_MAILPIT_URL (e.g.
 * http://localhost:8025) together with E2E_BASE_URL pointing at an instance
 * whose SMTP goes to that Mailpit; without it the email-dependent tests are
 * marked `test.fixme` because the default `go run` server has no SMTP
 * (email_enabled=false → /register redirects to /login).
 */
import { expect, type APIRequestContext, type TestInfo } from '@playwright/test'

export const MAILPIT = process.env.E2E_MAILPIT_URL?.replace(/\/$/, '') ?? ''

/** Unique per run and per Playwright project (chromium and mobile run every spec). */
export function uniqueEmail(info: TestInfo, prefix = 'cust'): string {
  const slug = info.project.name.replace(/[^a-z0-9]/gi, '').toLowerCase()
  return `${prefix}-${slug}-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}@e2e.test`
}

interface MailpitSummary {
  ID: string
  Subject: string
  To: { Address: string }[]
}

/** Polls Mailpit (≥ 1 s apart) for the newest message to `to` and returns the first link matching `pattern`. */
export async function linkFromMail(request: APIRequestContext, to: string, pattern: RegExp, timeoutMs = 20_000): Promise<string> {
  expect(MAILPIT, 'E2E_MAILPIT_URL is required').not.toBe('')
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const res = await request.get(`${MAILPIT}/api/v1/search?query=${encodeURIComponent(`to:"${to}"`)}`)
    if (res.ok()) {
      const { messages } = (await res.json()) as { messages: MailpitSummary[] }
      for (const m of messages ?? []) {
        const full = await (await request.get(`${MAILPIT}/api/v1/message/${m.ID}`)).json()
        const text = `${full.Text ?? ''}\n${full.HTML ?? ''}`.replace(/&amp;/g, '&')
        const hit = text.match(pattern)
        if (hit) return hit[0]
      }
    }
    await new Promise((r) => setTimeout(r, 1000))
  }
  throw new Error(`no mail to ${to} matching ${pattern} within ${timeoutMs} ms`)
}

/** Path + query of an absolute link (the test navigates within baseURL). */
export function pathOf(link: string): string {
  const u = new URL(link, 'http://placeholder')
  return u.pathname + u.search
}

