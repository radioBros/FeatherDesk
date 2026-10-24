// SPDX-License-Identifier: AGPL-3.0-only

/**
 * FE-011/FE-022/FE-033 E2E: an internal note never reaches the portal — not
 * in the page HTML, not in any WebSocket frame the customer receives.
 * Needs the full backend and a customer account (E2E_CUSTOMER_* or Mailpit).
 */
import { test, expect, fillEditor } from '../fixtures'
import { customerAccount, seedTeam, signIn, uid } from './helpers'

test.describe('internal notes stay internal', () => {
  test('the customer sees the public reply and never the note', async ({ browser, page }) => {
    const creds = await customerAccount(page)
    test.skip(!creds, 'needs E2E_CUSTOMER_EMAIL/E2E_CUSTOMER_PASSWORD or E2E_MAILPIT_URL')
    const seed = await seedTeam(browser)
    const id = uid()
    const ticket = await seed.api.post(
      '/agent/tickets',
      { subject: `Invoice question ${id}`, body_md: 'Where is my invoice?', requester_email: creds!.email, team_id: seed.team.id },
      { 'Idempotency-Key': `e2e-${id}` },
    )

    // The customer has the ticket open and records every WebSocket frame.
    const customer = await signIn(browser, 'customer', creds!)
    const frames: string[] = []
    customer.page.on('websocket', (ws) => ws.on('framereceived', (f) => frames.push(String(f.payload))))
    await customer.page.goto(`/tickets/${ticket.id}`)
    await expect(customer.page.getByTestId('message-thread')).toContainText('Where is my invoice?')

    const agent = await signIn(browser, 'staff', seed.l1)
    await agent.page.goto(`/agent/tickets/${ticket.number}`)
    const secret = `INTERNAL-${id} customer is on the late-payers list`
    await agent.page.getByTestId('mode-internal').click()
    await fillEditor(agent.page.getByTestId('composer-body'), secret)
    await agent.page.getByTestId('composer-send').click()
    await expect(agent.page.getByTestId('msg-internal')).toContainText(secret)

    const reply = `PUBLIC-${id} Your invoice is attached.`
    await agent.page.getByTestId('mode-public').click()
    await fillEditor(agent.page.getByTestId('composer-body'), reply)
    await agent.page.getByTestId('composer-send').click()

    // Live on the customer's open page, without reload.
    await expect(customer.page.getByTestId('message-thread')).toContainText(reply)
    expect(await customer.page.content()).not.toContain(secret)
    await customer.page.reload()
    await expect(customer.page.getByTestId('message-thread')).toContainText(reply)
    expect(await customer.page.content()).not.toContain(secret)
    expect(frames.join('\n')).not.toContain(secret)
    expect(frames.join('\n')).not.toContain(`INTERNAL-${id}`)

    await Promise.all([customer.context.close(), agent.context.close(), seed.admin.context.close()])
  })
})
