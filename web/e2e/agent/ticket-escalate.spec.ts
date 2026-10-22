// SPDX-License-Identifier: AGPL-3.0-only

/**
 * FE-022/FE-024/FE-044 E2E: an L1 agent replies and escalates; the L2 agent,
 * already on the ticket, sees the reply and the new tier live (no reload).
 * Needs the full backend (runs during integration).
 */
import { test, expect } from '../fixtures'
import { seedTeam, signIn } from './helpers'

test.describe('L1 → L2 escalation, live', () => {
  test.skip(({ isMobile }) => isMobile, 'two-agent flow is covered on desktop')

  test('L1 replies and escalates; L2 sees both without reloading', async ({ browser }) => {
    const seed = await seedTeam(browser)
    const ticket = await seed.api.post(
      '/agent/tickets',
      { subject: `Printer on fire ${seed.id}`, body_md: 'Smoke everywhere', requester_email: `ada-${seed.id}@e2e.test`, requester_name: 'Ada', team_id: seed.team.id },
      { 'Idempotency-Key': `e2e-${seed.id}` },
    )
    const l1 = await signIn(browser, 'staff', seed.l1)
    const l2 = await signIn(browser, 'staff', seed.l2)

    await l2.page.goto(`/agent/tickets/${ticket.number}`)
    await expect(l2.page.getByTestId('ticket-subject')).toHaveText(ticket.subject)
    let l2Reloaded = false
    l2.page.on('framenavigated', (f) => {
      if (f === l2.page.mainFrame()) l2Reloaded = true
    })

    await l1.page.goto(`/agent/tickets/${ticket.number}`)
    const reply = `We are on it ${seed.id}`
    await l1.page.getByTestId('composer-body').locator('textarea').fill(reply)
    await l1.page.getByTestId('composer-send').click()
    await expect(l1.page.getByTestId('message-thread')).toContainText(reply)

    // L2 sees the reply pushed over the ticket topic.
    await expect(l2.page.getByTestId('message-thread')).toContainText(reply)

    await l1.page.getByTestId('escalate-open').click()
    await l1.page.getByTestId('escalate-reason').locator('textarea').fill('Needs a specialist')
    await l1.page.getByTestId('escalate-submit').click()
    // L1 (tier 1) loses visibility and is sent back to the list.
    await expect(l1.page).toHaveURL(/\/agent\/tickets(\?|$)/)

    // L2 sees the tier change live, and the reason in the timeline.
    await expect(l2.page.getByTestId('ticket-header').getByTestId('tier-chip')).toHaveText('L2')
    await l2.page.getByTestId('tab-timeline').click()
    await expect(l2.page.getByTestId('timeline')).toContainText('Needs a specialist')
    expect(l2Reloaded).toBe(false)

    await Promise.all([l1.context.close(), l2.context.close(), seed.admin.context.close()])
  })
})
