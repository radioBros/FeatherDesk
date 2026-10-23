// SPDX-License-Identifier: AGPL-3.0-only

/**
 * D-21 / INT-018 E2E: the Slack integration end to end against e2e/fake-slack.mjs
 * (FD_SLACK_BASE_URL). The OAuth flow runs in a real popup that leaves for
 * another origin (so COOP severs window.opener) and reports back over
 * BroadcastChannel; then a rule is created and a new ticket is posted.
 */
import { test, expect, loginAs, ADMIN, tokenClient } from '../fixtures'

const fake = process.env.E2E_FAKE_SLACK_URL ?? 'http://127.0.0.1:18090'

test.describe('Slack integration', () => {
  test.skip(({ isMobile }) => isMobile, 'admin pages are covered on desktop')
  test.skip(!!process.env.E2E_BASE_URL && !process.env.E2E_FAKE_SLACK_URL, 'needs the fake Slack wired into the server')

  test('install through the popup, add a rule, a new ticket is posted', async ({ page, context }) => {
    await loginAs(page, 'staff', ADMIN)
    await page.goto('/agent/admin/integrations')
    await expect(page.getByTestId('integration-slack')).toContainText('Slack')
    await page.getByTestId('integration-slack').click()
    await expect(page).toHaveURL(/\/agent\/admin\/integrations\/slack$/)

    // A fresh instance shows the setup; a re-run may find it connected: start clean.
    const api = await tokenClient(page)
    if (await page.getByTestId('slack-disconnect').isVisible().catch(() => false)) {
      await page.getByTestId('slack-disconnect').click()
      await page.getByRole('button', { name: /confirm|ok|disconnect/i }).last().click()
    }
    await expect(page.getByTestId('slack-setup')).toBeVisible()
    await expect(page.getByTestId('slack-create-app')).toHaveAttribute('href', /manifest_json=/)

    // Empty submit: client-side validation only.
    await page.getByTestId('slack-client-id').locator('input').fill('not-an-id')
    await page.getByTestId('slack-app-save').click()
    await expect(page.getByTestId('slack-client-id')).toContainText(/format|valid/i)
    await page.getByTestId('slack-client-id').locator('input').fill('1234567890.0987654321')
    await page.getByTestId('slack-client-secret').locator('input').fill('e2e-secret')
    await page.getByTestId('slack-app-save').click()
    await expect(page.getByTestId('slack-connect')).toBeEnabled()

    // The popup: Slack's consent page (another origin), Allow, back to /agent/oauth/complete, closes itself.
    const popupPromise = context.waitForEvent('page')
    await page.getByTestId('slack-connect').click()
    const popup = await popupPromise
    await expect(page.getByTestId('oauth-waiting')).toBeVisible()
    await popup.waitForURL(/\/oauth\/v2\/authorize/)
    await expect(popup.locator('h1')).toHaveText('E2E Workspace')
    const closed = popup.waitForEvent('close')
    await popup.locator('#allow').click()
    await closed
    expect(page.url()).toMatch(/\/agent\/admin\/integrations\/slack$/) // the main tab never navigated
    await expect(page.getByTestId('slack-team')).toHaveText('E2E Workspace')

    // First rule from the empty state: new tickets to #support.
    await page.getByTestId('slack-rule-quickstart').click()
    await expect(page.getByTestId('slack-preview')).toContainText('New ticket from')
    await page.getByTestId('slack-route-channel').locator('.v-field').click()
    await page.getByTestId('slack-channel-support').click()
    await page.getByTestId('slack-route-save').click()
    await expect(page.getByTestId('slack-route-card')).toContainText('support')

    // A new ticket reaches the channel.
    const id = Date.now().toString(36)
    const subject = `Slack e2e ${id}`
    const res = await api.post('/agent/tickets', { subject, body_md: 'Posted to Slack?', requester_email: `slack-${id}@e2e.test`, priority: 'high' })
    expect(res.status()).toBe(201)
    await expect
      .poll(async () => {
        const posts = (await (await page.request.get(`${fake}/e2e/posts`)).json()) as { channel: string; text: string }[]
        return posts.some((p) => p.channel === 'C0SUPPORT' && p.text.includes(subject))
      }, { timeout: 15_000 })
      .toBe(true)
  })
})
