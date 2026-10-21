// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Two-step verification end to end (D-19 §B, FE-075): a dedicated local staff
 * account (never the shared admin, and the instance policy is not touched)
 * enrols an authenticator app from its profile, the test computes the TOTP
 * codes itself from the secret shown (RFC 6238), then signs in with TOTP, with
 * a recovery code and, when Mailpit is available, with an e-mailed code.
 */
import { createHmac } from 'node:crypto'
import type { Browser, Page } from '@playwright/test'
import { test, expect, ADMIN, clientIp, type Credentials } from './fixtures'
import { apiFor, signIn, uid } from './agent/helpers'

function base32(s: string): Buffer {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  let bits = ''
  for (const c of s.replace(/[\s=]/g, '').toUpperCase()) bits += alphabet.indexOf(c).toString(2).padStart(5, '0')
  const out: number[] = []
  for (let i = 0; i + 8 <= bits.length; i += 8) out.push(parseInt(bits.slice(i, i + 8), 2))
  return Buffer.from(out)
}

/** RFC 6238 (HMAC-SHA1, 30 s, 6 digits) for time step `step`. */
function totp(secret: string, step: number): string {
  const msg = Buffer.alloc(8)
  msg.writeBigUInt64BE(BigInt(step))
  const sum = createHmac('sha1', base32(secret)).update(msg).digest()
  const off = sum[sum.length - 1]! & 0x0f
  return ((sum.readUInt32BE(off) & 0x7fffffff) % 1_000_000).toString().padStart(6, '0')
}
const nowStep = () => Math.floor(Date.now() / 1000 / 30)

/** Creates a local agent through the admin API; returns its credentials. */
async function dedicatedStaff(browser: Browser): Promise<Credentials> {
  const admin = await signIn(browser, 'staff', ADMIN)
  const api = await apiFor(admin.page)
  const id = uid()
  const creds = { email: `mfa-${id}@e2e.test`, password: `e2e-mfa-password-${id}` }
  await api.post('/admin/staff', { email: creds.email, name: `MFA Agent ${id}`, role: 'agent', tier: 1, password: creds.password })
  await admin.context.close()
  return creds
}

async function passwordStep(page: Page, creds: Credentials) {
  await page.goto('/agent/login')
  await page.getByTestId('login-email').locator('input').fill(creds.email)
  await page.getByTestId('login-password').locator('input').fill(creds.password)
  await page.getByTestId('login-submit').click()
}

async function signOut(page: Page) {
  await page.context().clearCookies()
}

test('enrol TOTP, then sign in with TOTP, a recovery code and (with Mailpit) an e-mailed code', async ({ browser }) => {
  test.setTimeout(90_000)
  const creds = await dedicatedStaff(browser)
  const context = await browser.newContext({ extraHTTPHeaders: clientIp() })
  const page = await context.newPage()

  // 1. No second factor yet: the password signs in directly.
  await passwordStep(page, creds)
  await expect(page).toHaveURL(/\/agent\/tickets/)

  // 2. Enrol from the profile: password confirmation, QR + secret, code, recovery codes.
  await page.goto('/agent/profile')
  await page.getByTestId('mfa-totp-setup').click()
  await page.getByTestId('mfa-reauth-password').locator('input').fill(creds.password)
  await page.getByTestId('mfa-reauth-submit').click()
  await expect(page.getByTestId('mfa-qr')).toBeVisible()
  const secret = (await page.getByTestId('mfa-secret').textContent())!.trim()
  expect(secret).toMatch(/^[A-Z2-7]{32}$/)
  const enrolStep = nowStep()
  await page.getByTestId('mfa-setup-dialog').getByTestId('mfa-code').locator('input').fill(totp(secret, enrolStep))
  await page.getByTestId('mfa-setup-dialog').getByTestId('mfa-code-submit').click()
  await expect(page.getByTestId('mfa-recovery-code')).toHaveCount(10)
  const codes = (await page.getByTestId('mfa-recovery-code').allTextContents()).map((c) => c.trim())
  expect(codes[0]).toMatch(/^[a-z2-9]{4}-[a-z2-9]{4}-[a-z2-9]{4}$/)
  await page.getByTestId('mfa-codes-done').click()
  await expect(page.getByTestId('mfa-row-totp')).toContainText('On')

  // 3. Password alone no longer signs in: the TOTP step does. The next step's
  //    code is inside the ±1 window and newer than the one just used (replay guard).
  await signOut(page)
  await passwordStep(page, creds)
  await expect(page.getByTestId('mfa-step')).toBeVisible()
  expect((await page.request.get('/api/v1/me')).status()).toBe(401)
  await page.getByTestId('mfa-code').locator('input').fill(totp(secret, Math.max(enrolStep + 1, nowStep())))
  await page.getByTestId('mfa-code-submit').click()
  await expect(page).toHaveURL(/\/agent\/tickets/)

  // 4. A recovery code works once.
  await signOut(page)
  await passwordStep(page, creds)
  await page.getByTestId('mfa-method-recovery').click()
  await page.getByTestId('mfa-code').locator('input').fill(codes[0]!)
  await page.getByTestId('mfa-code-submit').click()
  await expect(page).toHaveURL(/\/agent\/tickets/)
  await signOut(page)
  await passwordStep(page, creds)
  await page.getByTestId('mfa-method-recovery').click()
  await page.getByTestId('mfa-code').locator('input').fill(codes[0]!)
  await page.getByTestId('mfa-code-submit').click()
  await expect(page.getByTestId('mfa-code-form')).toContainText(/not valid|wrong|expired/i)

  // 5. E-mailed code through Mailpit (only when the server has SMTP).
  const mailpit = process.env.E2E_MAILPIT_URL?.trim()
  if (mailpit) {
    await page.getByTestId('mfa-method-recovery').click()
    await page.getByTestId('mfa-code').locator('input').fill(codes[1]!)
    await page.getByTestId('mfa-code-submit').click()
    await expect(page).toHaveURL(/\/agent\/tickets/)
    await page.goto('/agent/profile')
    await page.getByTestId('mfa-email-enable').click()
    await page.getByTestId('mfa-reauth-password').locator('input').fill(creds.password)
    await page.getByTestId('mfa-reauth-submit').click()
    await expect(page.getByTestId('mfa-row-email')).toContainText('On')
    await signOut(page)
    const since = new Date()
    await passwordStep(page, creds)
    await page.getByTestId('mfa-method-email').click()
    await page.getByTestId('mfa-send-email').click()
    await expect(page.getByTestId('mfa-email-sent')).toBeVisible()
    let code: string | null = null
    for (let i = 0; i < 30 && !code; i++) {
      const list = await (await page.request.get(`${mailpit}/api/v1/search?query=${encodeURIComponent('to:' + creds.email)}`)).json()
      for (const m of list.messages ?? []) {
        if (new Date(m.Created) < since) continue
        const msg = await (await page.request.get(`${mailpit}/api/v1/message/${m.ID}`)).json()
        code = /\b(\d{6})\b/.exec(String(msg.Text ?? ''))?.[1] ?? null
        if (code) break
      }
      if (!code) await page.waitForTimeout(500)
    }
    expect(code, 'MFA code mail in Mailpit').not.toBeNull()
    await page.getByTestId('mfa-code').locator('input').fill(code!)
    await page.getByTestId('mfa-code-submit').click()
    await expect(page).toHaveURL(/\/agent\/tickets/)
  } else {
    test.info().annotations.push({ type: 'skip-part', description: 'e-mail code: E2E_MAILPIT_URL not set' })
  }
  await context.close()
})
