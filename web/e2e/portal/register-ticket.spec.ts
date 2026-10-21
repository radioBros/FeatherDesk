// SPDX-License-Identifier: AGPL-3.0-only

/**
 * FE-013…FE-018 end to end: register → verify (link from Mailpit) → sign in →
 * open a ticket → reply → "my issue is solved". Needs SMTP → Mailpit.
 */
import { test, expect, watchConsole } from '../fixtures'
import { MAILPIT, linkFromMail, pathOf, uniqueEmail } from './helpers'

test.describe('customer portal: register to resolve', () => {
  test.describe.configure({ mode: 'serial' })
  test.fixme(!MAILPIT, 'needs E2E_MAILPIT_URL + an instance with SMTP to Mailpit (dev compose, DEP-004) to read the verification link')

  test('register, verify, open a ticket, reply and resolve', async ({ page }, info) => {
    const problems = watchConsole(page)
    const email = uniqueEmail(info)
    const password = 'e2e-customer-password-1'

    await page.goto('/register')
    await page.getByTestId('register-name').locator('input').fill('E2E Customer')
    await page.getByTestId('register-email').locator('input').fill(email)
    await page.getByTestId('register-password').locator('input').fill(password)
    await page.getByTestId('register-confirm').locator('input').fill(password)
    await page.getByTestId('register-submit').click()
    await expect(page.getByTestId('register-done')).toContainText(email)

    // The text part is Markdown (`[Verify](url)`): the token is base64url, never the closing `)`.
    const link = await linkFromMail(page.request, email, /https?:\/\/[^\s"'<>()]+\/verify-email\?token=[\w-]+/)
    await page.goto(pathOf(link))
    await expect(page.getByTestId('verify-done')).toBeVisible()

    // Verification may already sign in (then /login redirects); otherwise sign in with the password.
    if (!(await page.request.get('/api/v1/me')).ok()) {
      await page.goto('/login')
      await page.getByTestId('login-email').locator('input').fill(email)
      await page.getByTestId('login-password').locator('input').fill(password)
      await page.getByTestId('login-submit').click()
    } else {
      await page.goto('/tickets')
    }
    await expect(page).toHaveURL(/\/tickets$/)

    // New ticket: empty submit is blocked client-side, then a real one opens the detail page.
    await page.goto('/tickets/new')
    let posted = false
    page.on('request', (r) => {
      if (r.method() === 'POST' && r.url().endsWith('/api/v1/portal/tickets')) posted = true
    })
    await page.getByTestId('nt-submit').click()
    await expect(page.getByText('This field is required').first()).toBeVisible()
    expect(posted).toBe(false)
    await page.getByTestId('nt-subject').locator('input').fill('E2E: printer offline')
    await page.getByTestId('md-input').locator('textarea').fill('Since this morning nobody can print. Error **E-301**.')
    await page.getByTestId('nt-submit').click()
    await expect(page).toHaveURL(/\/tickets\/[0-9a-f-]{36}$/)
    await expect(page.getByTestId('detail-subject')).toContainText('E2E: printer offline')

    // Reply
    await page.getByTestId('md-input').locator('textarea').fill('Adding more details: it is the 2nd floor printer.')
    await page.getByTestId('reply-submit').click()
    await expect(page.getByTestId('message-thread')).toContainText('2nd floor printer')

    // Resolve ("my issue is solved" exists iff allowed_actions has resolve)
    await page.getByTestId('resolve-btn').click()
    await page.getByTestId('confirm-ok').click()
    await expect(page.getByTestId('status-chip')).toHaveText('Resolved')

    expect(problems).toEqual([])
  })
})
