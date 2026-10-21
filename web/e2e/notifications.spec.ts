// SPDX-License-Identifier: AGPL-3.0-only

/**
 * D-19 §D E2E (FE-090…FE-096): the notification centre against the real
 * backend.
 *  - an agent assigned a ticket sees the bell badge go up live (no reload);
 *  - marking it read in one tab updates a second tab of the same user;
 *  - with the browser channel on and the notification permission granted in
 *    the Playwright context, a hidden page raises a browser notification,
 *    and clicking it opens the ticket.
 * Headless Chromium never really hides a page, so an init script overrides
 * `document.visibilityState` and records every `new Notification(…)` made
 * through the real constructor. Headless Chromium also keeps
 * `Notification.permission` at "denied" although the context grant shows in
 * `navigator.permissions` (checked 2026-10-21, Playwright 1.64.1), so the
 * recorder reports the Permissions API state: the grant still comes from
 * `context.grantPermissions(['notifications'])`.
 */
import type { Page } from '@playwright/test'
import { test, expect } from './fixtures'
import { apiFor, seedTeam, signIn, uid } from './agent/helpers'

const HIDDEN_AND_RECORDED = () => {
  const w = window as unknown as {
    __hidden: boolean
    __notifs: { title: string; options?: NotificationOptions; n: Notification }[]
    __setHidden: (h: boolean) => void
  }
  w.__hidden = false
  w.__notifs = []
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => (w.__hidden ? 'hidden' : 'visible') })
  Object.defineProperty(document, 'hidden', { configurable: true, get: () => w.__hidden })
  w.__setHidden = (h: boolean) => {
    w.__hidden = h
    document.dispatchEvent(new Event('visibilitychange'))
  }
  const Real = window.Notification
  if (!Real) return
  const Recorder = function (this: unknown, title: string, options?: NotificationOptions) {
    const n = new Real(title, options)
    w.__notifs.push({ title, options, n })
    return n
  } as unknown as typeof Notification
  let granted = false
  void navigator.permissions?.query({ name: 'notifications' }).then((s) => (granted = s.state === 'granted'))
  Object.defineProperty(Recorder, 'permission', { get: () => (granted ? 'granted' : Real.permission) })
  Recorder.requestPermission = Real.requestPermission.bind(Real)
  window.Notification = Recorder
}

const badge = (page: Page) => page.getByTestId('notification-count')

async function unread(page: Page): Promise<number> {
  const r = await page.request.get('/api/v1/me/notifications/unread-count')
  return (await r.json()).unread_count as number
}

test.describe('notification centre', () => {
  test.skip(({ isMobile }) => isMobile, 'multi-tab and browser-notification flows are covered on desktop')

  test('an assigned agent sees the bell badge go up live; mark read syncs a second tab', async ({ browser }) => {
    const seed = await seedTeam(browser)
    const agent = await signIn(browser, 'staff', seed.l1)
    const me = (await apiFor(agent.page)).me
    const ticket = await seed.api.post(
      '/agent/tickets',
      { subject: `Bell ${seed.id}`, body_md: 'x', requester_email: `bell-${seed.id}@e2e.test`, requester_name: 'Bell', team_id: seed.team.id },
      { 'Idempotency-Key': `e2e-bell-${seed.id}` },
    )
    // The creation itself notified the team (staff.ticket_new); wait until it is stored.
    await expect.poll(() => unread(agent.page)).toBeGreaterThan(0)
    const before = await unread(agent.page)

    const tabA = agent.page
    await tabA.goto('/agent/tickets')
    await expect(badge(tabA)).toHaveText(String(before))
    let reloaded = false
    tabA.on('framenavigated', (f) => {
      if (f === tabA.mainFrame()) reloaded = true
    })
    const tabB = await agent.context.newPage()
    await tabB.goto('/agent/notifications')
    await expect(tabB.getByTestId('notifications-page')).toBeVisible()
    await expect(badge(tabB)).toHaveText(String(before))

    await seed.api.patch(`/agent/tickets/${ticket.id}`, { assignee_id: me.id })
    await expect(badge(tabA)).toHaveText(String(before + 1))
    await expect(badge(tabB)).toHaveText(String(before + 1))
    // The page in tab B shows the new entry on top, unread.
    const first = tabB.getByTestId('notification-item').first()
    await expect(first).toContainText(`assigned #${ticket.number} to you`)
    await expect(first).toHaveAttribute('data-unread', 'true')
    expect(reloaded).toBe(false)

    // Tab A: open the dropdown and mark everything read → tab B follows.
    await tabA.getByTestId('notification-bell').click()
    await expect(tabA.getByTestId('notification-menu')).toContainText(`#${ticket.number}`)
    await tabA.getByTestId('notification-read-all').click()
    await expect(badge(tabA)).toBeHidden()
    await expect(badge(tabB)).toBeHidden()
    await expect(tabB.getByTestId('notification-item').first()).toHaveAttribute('data-unread', 'false')
    expect(await unread(tabB)).toBe(0)
    expect(reloaded).toBe(false)

    await Promise.all([agent.context.close(), seed.admin.context.close()])
  })

  test('a hidden page raises a browser notification; clicking it opens the ticket', async ({ browser, baseURL }) => {
    const seed = await seedTeam(browser)
    const agent = await signIn(browser, 'staff', seed.l2)
    await agent.context.grantPermissions(['notifications'], { origin: baseURL })
    await agent.context.addInitScript(HIDDEN_AND_RECORDED)
    const page = agent.page
    const me = (await apiFor(page)).me

    // Turn the browser channel on for "assigned to me" in the preferences matrix.
    await page.goto('/agent/profile')
    await expect(page.getByTestId('prefs-permission-granted')).toBeVisible()
    await page.getByTestId('prefs-staff.assigned-browser').locator('input').check()
    await page.getByTestId('prefs-save').click()
    await expect.poll(async () => (await (await page.request.get('/api/v1/me')).json()).notification_prefs['staff.assigned'].browser).toBe(true)

    const ticket = await seed.api.post(
      '/agent/tickets',
      { subject: `Pop-up ${uid()}`, body_md: 'x', requester_email: `pop-${seed.id}@e2e.test`, requester_name: 'Pop', team_id: seed.team.id },
      { 'Idempotency-Key': `e2e-pop-${seed.id}` },
    )
    await page.goto('/agent/tickets')
    await expect(page.getByTestId('notification-bell')).toBeVisible()

    // Visible page: the assignment updates the badge but raises no pop-up.
    await seed.api.patch(`/agent/tickets/${ticket.id}`, { assignee_id: me.id })
    await expect.poll(() => unread(page)).toBeGreaterThan(0)
    await expect(badge(page)).toHaveText(String(await unread(page)))
    expect(await page.evaluate(() => (window as unknown as { __notifs: unknown[] }).__notifs.length)).toBe(0)
    await (await apiFor(page)).post('/me/notifications/read-all', {})

    // Hidden page: unassign + assign again → one browser notification.
    await page.evaluate(() => (window as unknown as { __setHidden: (h: boolean) => void }).__setHidden(true))
    await seed.api.patch(`/agent/tickets/${ticket.id}`, { assignee_id: null })
    await seed.api.patch(`/agent/tickets/${ticket.id}`, { assignee_id: me.id })
    await expect
      .poll(() => page.evaluate(() => (window as unknown as { __notifs: { title: string }[] }).__notifs.map((n) => n.title)))
      .toEqual([expect.stringContaining(`assigned #${ticket.number} to you`)])
    const tag = await page.evaluate(() => (window as unknown as { __notifs: { options?: NotificationOptions }[] }).__notifs[0]!.options?.tag)
    expect(tag).toBeTruthy()

    // Clicking it focuses the tab and opens the ticket (and marks it read).
    await page.evaluate(() => {
      const w = window as unknown as { __notifs: { n: Notification }[]; __setHidden: (h: boolean) => void }
      w.__setHidden(false)
      w.__notifs[0]!.n.onclick?.(new Event('click'))
    })
    await expect(page).toHaveURL(new RegExp(`/agent/tickets/${ticket.number}$`))
    await expect(page.getByTestId('ticket-subject')).toHaveText(ticket.subject)
    await expect
      .poll(async () => (await (await page.request.get('/api/v1/me/notifications?unread=true&type=staff.assigned')).json()).items.length)
      .toBe(0)

    await Promise.all([agent.context.close(), seed.admin.context.close()])
  })
})
