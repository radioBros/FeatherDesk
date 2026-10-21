// SPDX-License-Identifier: AGPL-3.0-only

/**
 * FE-033: the M2 milestone flow end to end against the real binary.
 *
 * A customer registers (verification link from Mailpit), opens a ticket with
 * an attachment; the L1 agent replies, adds an internal note and escalates;
 * the L2 agent, already on the ticket, sees all of it live and resolves it.
 * The customer's page, open the whole time, receives the public replies and
 * the Resolved status over the WebSocket without a reload, and the internal
 * note appears in neither its HTML nor any WebSocket frame it received.
 *
 * Needs SMTP → Mailpit (E2E_MAILPIT_URL). Staff sign in with local passwords
 * (ST_AUTH_STAFF_LOCAL); the SSO login itself is covered by sso.spec.ts.
 */
import { test, expect, watchConsole } from './fixtures'
import { seedTeam, signIn, uid } from './agent/helpers'
import { MAILPIT, linkFromMail, pathOf, uniqueEmail } from './portal/helpers'

// 1×1 transparent PNG.
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=', 'base64')

test.describe('M2 ticket loop', () => {
  test.skip(!MAILPIT, 'needs E2E_MAILPIT_URL and SMTP to Mailpit to read the verification link')

  test('register → ticket → L1 reply, note, escalate → L2 resolves; the customer sees it live, never the note', async ({ page, browser }, info) => {
    test.setTimeout(120_000)
    const problems = watchConsole(page)
    const id = uid()
    const seed = await seedTeam(browser)
    const category = await seed.api.post('/admin/categories', { name: `M2 ${id}`, default_team_id: seed.team.id })

    // Every frame the customer's page receives, on every socket it opens (reconnects included).
    const frames: string[] = []
    let sockets = 0
    page.on('websocket', (ws) => {
      sockets++
      ws.on('framereceived', (f) => frames.push(typeof f.payload === 'string' ? f.payload : f.payload.toString('utf8')))
    })

    // 1. Register and verify through the link in the email.
    const email = uniqueEmail(info, 'm2')
    const password = `e2e-customer-password-${id}`
    await page.goto('/register')
    await page.getByTestId('register-name').locator('input').fill('M2 Customer')
    await page.getByTestId('register-email').locator('input').fill(email)
    await page.getByTestId('register-password').locator('input').fill(password)
    await page.getByTestId('register-confirm').locator('input').fill(password)
    await page.getByTestId('register-submit').click()
    await expect(page.getByTestId('register-done')).toContainText(email)
    const link = await linkFromMail(page.request, email, /https?:\/\/[^\s"'<>()]+\/verify-email\?token=[\w-]+/)
    await page.goto(pathOf(link))
    await expect(page.getByTestId('verify-done')).toBeVisible()
    if (!(await page.request.get('/api/v1/me')).ok()) {
      await page.goto('/login')
      await page.getByTestId('login-email').locator('input').fill(email)
      await page.getByTestId('login-password').locator('input').fill(password)
      await page.getByTestId('login-submit').click()
    } else {
      await page.goto('/tickets')
    }
    await expect(page).toHaveURL(/\/tickets$/)

    // 2. A new ticket with an attachment, in the category routed to the seeded team.
    const subject = `M2 printer on fire ${id}`
    await page.goto('/tickets/new')
    await page.getByTestId('nt-subject').locator('input').fill(subject)
    await page.getByTestId('nt-category').click()
    await page.getByRole('option', { name: `M2 ${id}` }).click()
    await page.getByTestId('md-input').locator('textarea').fill(`CUSTOMER-${id} The 2nd floor printer smokes.`)
    await page.getByTestId('file-input').setInputFiles({ name: `photo-${id}.png`, mimeType: 'image/png', buffer: PNG })
    await expect(page.getByTestId('upload-item')).toContainText(`photo-${id}.png`)
    await expect(page.getByTestId('upload-error')).toHaveCount(0)
    await page.getByTestId('nt-submit').click()
    // The portal opens a new ticket by its ref (KEY-123, D-20).
    await expect(page).toHaveURL(/\/tickets\/[A-Z][A-Z0-9]*-\d+$/)
    const ticketRef = page.url().split('/').pop()!
    await expect(page.getByTestId('detail-subject')).toContainText(subject)
    await expect(page.getByTestId('attachment-list')).toContainText(`photo-${id}.png`)
    // From here on the customer's page must never reload: a marker on window survives only without one.
    await page.evaluate(() => ((window as unknown as { __m2: string }).__m2 = 'alive'))

    const ticket = await seed.api.get(`/agent/tickets/by-ref/${ticketRef}`)
    const ticketId: string = ticket.id
    expect(ticket.category_id).toBe(category.id)
    expect(ticket.team_id).toBe(seed.team.id)

    // 3. L2 already has the ticket open (tier 1, own team) before anything happens.
    const l2 = await signIn(browser, 'staff', seed.l2)
    await l2.page.goto(`/agent/tickets/${ticket.number}`)
    await expect(l2.page.getByTestId('ticket-subject')).toHaveText(subject)
    await l2.page.evaluate(() => ((window as unknown as { __m2: string }).__m2 = 'alive'))

    // 4. L1: public reply, internal note, escalation with a reason.
    const l1 = await signIn(browser, 'staff', seed.l1)
    await l1.page.goto(`/agent/tickets/${ticket.number}`)
    await expect(l1.page.getByTestId('message-thread')).toContainText(`CUSTOMER-${id}`)
    await expect(l1.page.getByTestId('attachment-list').first()).toContainText(`photo-${id}.png`)
    const reply = `PUBLIC-${id} We are sending a technician.`
    await l1.page.getByTestId('composer-body').locator('textarea').fill(reply)
    await l1.page.getByTestId('composer-send').click()
    await expect(l1.page.getByTestId('message-thread')).toContainText(reply)

    const noteToken = `INTERNAL-${id}`
    const note = `${noteToken} the customer skipped the last two maintenance visits`
    await l1.page.getByTestId('mode-internal').click()
    await l1.page.getByTestId('composer-body').locator('textarea').fill(note)
    await l1.page.getByTestId('composer-send').click()
    await expect(l1.page.getByTestId('msg-internal')).toContainText(note)

    await l1.page.getByTestId('escalate-open').click()
    await l1.page.getByTestId('escalate-reason').locator('textarea').fill(`Needs hardware specialist ${id}`)
    await l1.page.getByTestId('escalate-submit').click()
    await expect(l1.page).toHaveURL(/\/agent\/tickets(\?|$)/)

    // 5. L2 sees the reply, the note and the new tier live; the timeline has the reason.
    await expect(l2.page.getByTestId('message-thread')).toContainText(reply)
    await expect(l2.page.getByTestId('msg-internal')).toContainText(note)
    await expect(l2.page.getByTestId('ticket-header').getByTestId('tier-chip')).toHaveText('L2')
    await l2.page.getByTestId('tab-timeline').click()
    await expect(l2.page.getByTestId('timeline')).toContainText(`Needs hardware specialist ${id}`)
    await l2.page.getByTestId('tab-thread').click()

    // The customer got the public reply pushed, without reload.
    await expect(page.getByTestId('message-thread')).toContainText(reply)

    // 6. L2 answers and resolves in one step.
    const fix = `RESOLVED-${id} The technician replaced the fuser.`
    await l2.page.getByTestId('composer-body').locator('textarea').fill(fix)
    await l2.page.getByTestId('composer-send-menu').click()
    await l2.page.getByTestId('send-status-resolved').click()
    await expect(l2.page.getByTestId('ticket-header').getByTestId('status-chip')).toHaveText('Resolved')
    expect(await l2.page.evaluate(() => (window as unknown as { __m2?: string }).__m2)).toBe('alive')

    // 7. The customer's open page: the answer and the Resolved status arrive live.
    await expect(page.getByTestId('message-thread')).toContainText(fix)
    await expect(page.getByTestId('status-chip').first()).toHaveText('Resolved')
    expect(await page.evaluate(() => (window as unknown as { __m2?: string }).__m2)).toBe('alive')

    // Never the note: not in the live page, not in any frame, not after a reload.
    const html = await page.content()
    expect(html).toContain(reply)
    expect(html).not.toContain(noteToken)
    expect(sockets).toBeGreaterThan(0)
    const all = frames.join('\n')
    // Positive control: the frames carried the live updates (else "no note" would be vacuous).
    const events = frames.map((f) => JSON.parse(f) as { event?: string; data?: unknown })
    const carried = (event: string, text: string) => events.some((e) => e.event === event && JSON.stringify(e.data).includes(text))
    expect(carried('message.created', `PUBLIC-${id}`)).toBe(true)
    expect(carried('message.created', `RESOLVED-${id}`)).toBe(true)
    expect(carried('ticket.status_changed', '"to":"resolved"')).toBe(true)
    expect(all).not.toContain(noteToken)
    expect(all).not.toContain('maintenance visits')
    await page.reload()
    await expect(page.getByTestId('message-thread')).toContainText(fix)
    const thread = await page.getByTestId('message-thread').innerText()
    expect(thread).toContain(reply)
    expect(thread).toContain(`CUSTOMER-${id}`)
    expect(await page.content()).not.toContain(noteToken)
    // The portal API never returns the note either (I-2).
    const portal = await page.request.get(`/api/v1/portal/tickets/${ticketId}`)
    expect(portal.ok()).toBe(true)
    const body = await portal.text()
    expect(body).toContain(`PUBLIC-${id}`)
    expect(body).not.toContain(noteToken)

    expect(problems).toEqual([])
    await Promise.all([l1.context.close(), l2.context.close(), seed.admin.context.close()])
  })
})
