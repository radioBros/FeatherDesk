// SPDX-License-Identifier: AGPL-3.0-only

/**
 * D-19 A / FE-067 E2E: automation and macros against the full backend.
 * 1. A `ticket.created` rule routes a new portal ticket to a team, assigns it
 *    round-robin and tags it. Rules run in the durable queue consumer, so the
 *    result is polled. The rule matches only this test's unique subject marker
 *    and is disabled afterwards, so parallel tests are never affected.
 * 2. An agent applies a personal macro from the ticket detail: note, tag and
 *    status change in one click.
 */
import { test, expect } from './fixtures'
import { apiFor, customerAccount, seedTeam, signIn, uid } from './agent/helpers'

interface TicketLite {
  id: string
  number: number
  status: string
  team_id: string | null
  assignee: { id: string; name: string } | null
  tags?: string[]
}

test.describe('automation (D-19 A)', () => {
  test('a rule routes, assigns and tags a new portal ticket', async ({ browser, page }) => {
    const creds = await customerAccount(page)
    test.skip(!creds, 'needs E2E_CUSTOMER_EMAIL/E2E_CUSTOMER_PASSWORD or E2E_MAILPIT_URL')
    const seed = await seedTeam(browser)
    const id = uid()
    const marker = `AUTO-${id}`
    const tag = `auto-${id}`.toLowerCase()
    const rule = await seed.api.post('/admin/automation/rules', {
      name: `E2E routing ${id}`,
      event: 'ticket.created',
      match: 'all',
      conditions: [{ field: 'subject', op: 'contains', value: marker }],
      actions: [
        { type: 'set_team', value: seed.team.id },
        { type: 'assign', strategy: 'round_robin' },
        { type: 'add_tag', value: tag },
      ],
      enabled: true,
      stop_processing: false,
    })
    try {
      // The admin sees the rule in the rules page.
      await seed.admin.page.goto('/agent/admin/automation')
      await expect(seed.admin.page.getByTestId('rules-table')).toContainText(`E2E routing ${id}`)

      const customer = await signIn(browser, 'customer', creds!)
      const capi = await apiFor(customer.page)
      const created = (await capi.post('/portal/tickets', { subject: `Outage ${marker}`, body_md: 'Nothing loads.' }, { 'Idempotency-Key': `e2e-${id}` })) as { id: string; number: number }

      let ticket: TicketLite | null = null
      await expect
        .poll(
          async () => {
            ticket = (await seed.api.get(`/agent/tickets/${created.id}`)) as TicketLite
            return { team: ticket.team_id, assigned: !!ticket.assignee, tagged: (ticket.tags ?? []).includes(tag) }
          },
          { timeout: 15_000 },
        )
        .toEqual({ team: seed.team.id, assigned: true, tagged: true })

      // The agent UI shows the tag and the automation entry in the timeline.
      await seed.admin.page.goto(`/agent/tickets/${ticket!.number}`)
      await expect(seed.admin.page.getByText(tag).first()).toBeVisible()
      await seed.admin.page.getByTestId('tab-timeline').click()
      await expect(seed.admin.page.getByTestId('timeline')).toContainText('Automation rule applied')
      await expect(seed.admin.page.getByTestId('timeline')).toContainText(`E2E routing ${id}`)
      await customer.context.close()
    } finally {
      await seed.api.patch(`/admin/automation/rules/${rule.id}`, { enabled: false })
      await seed.admin.context.close()
    }
  })

  test('an agent applies a macro from the ticket detail', async ({ browser }) => {
    const seed = await seedTeam(browser)
    const id = uid()
    const ticket = (await seed.api.post(
      '/agent/tickets',
      { subject: `Macro ${id}`, body_md: 'Please help.', requester_email: `macro-${id}@e2e.test`, requester_name: 'Mac Customer', team_id: seed.team.id },
      { 'Idempotency-Key': `e2e-${id}` },
    )) as TicketLite
    const agent = await signIn(browser, 'staff', seed.l1)
    const aapi = await apiFor(agent.page)
    const note = `MACRO-${id} asked for logs`
    const macroTag = `macro-${id}`.toLowerCase()
    await aapi.post('/agent/macros', { name: `Ask for logs ${id}`, reply_kind: 'internal', reply_md: note, status: 'pending', add_tags: [macroTag], assignee: 'me' })

    await agent.page.goto(`/agent/tickets/${ticket.number}`)
    await agent.page.getByTestId('macro-picker').click()
    await agent.page.getByTestId('macro-item').filter({ hasText: `Ask for logs ${id}` }).click()
    await expect(agent.page.getByTestId('message-thread')).toContainText(note)

    await expect
      .poll(async () => {
        const t = (await aapi.get(`/agent/tickets/${ticket.id}`)) as TicketLite
        return { status: t.status, tagged: (t.tags ?? []).includes(macroTag), mine: t.assignee?.id === aapi.me.id }
      })
      .toEqual({ status: 'pending', tagged: true, mine: true })

    await Promise.all([agent.context.close(), seed.admin.context.close()])
  })
})
