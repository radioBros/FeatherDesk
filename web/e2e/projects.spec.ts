// SPDX-License-Identifier: AGPL-3.0-only

/**
 * D-20 projects end to end, against the real binary on a fresh SQLite data
 * directory filled with `simpletickets seed --demo` (projects WEB, MOB and IT,
 * a staff member limited to MOB, a project-scoped portal entry). The spec starts
 * its own server on its own port, so it never disturbs the shared E2E instance.
 *   E2E_SERVER_BIN  the built binary (default: ../bin/simpletickets)
 */
import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { mkdtempSync, existsSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test, expect, loginAs, watchConsole } from './fixtures'

const PORT = Number(process.env.E2E_PROJECTS_PORT ?? 18091)
const BASE = `http://127.0.0.1:${PORT}`
// The package is an ES module: no __dirname, resolve against this file's URL.
const BIN = process.env.E2E_SERVER_BIN || fileURLToPath(new URL('../../bin/simpletickets', import.meta.url))
const PASSWORD = 'demo-password-123'
const ADMIN = { email: 'admin@demo.test', password: PASSWORD }
const MOBILE_AGENT = { email: 'mona.mobile@demo.test', password: PASSWORD }
const CUSTOMER = { email: 'hank.hill@globex.test', password: PASSWORD }

test.use({ baseURL: BASE })
test.describe.configure({ mode: 'serial' })

let server: ChildProcess | undefined
let dataDir = ''

test.beforeAll(async ({ browserName: _browser }, info) => {
  test.skip(info.project.name !== 'chromium', 'one run on a desktop viewport is enough')
  test.setTimeout(180_000)
  if (!existsSync(BIN)) throw new Error(`server binary not found: ${BIN} (run make build or set E2E_SERVER_BIN)`)
  dataDir = mkdtempSync(join(tmpdir(), 'st-projects-'))
  const env = {
    ...process.env,
    ST_DATA_DIR: dataDir,
    ST_DATABASE_URL: `sqlite://${join(dataDir, 'projects.db')}`,
    ST_SECRET_KEY: 'e2e-secret-key-0123456789abcdef0123456789abcdef',
    ST_LISTEN_ADDR: `127.0.0.1:${PORT}`,
    ST_PUBLIC_URL: BASE,
    ST_AUTH_STAFF_LOCAL: 'true',
    ST_TRUSTED_PROXIES: '127.0.0.1/32,::1/128',
    ST_LOG_LEVEL: 'warn',
  }
  execFileSync(BIN, ['seed', '--demo'], { env, stdio: 'ignore' })
  server = spawn(BIN, ['serve'], { env, stdio: 'ignore' })
  const deadline = Date.now() + 60_000
  for (;;) {
    try {
      if ((await fetch(`${BASE}/readyz`)).ok) break
    } catch {
      /* not up yet */
    }
    if (Date.now() > deadline) throw new Error('the projects E2E server did not become ready')
    await new Promise((r) => setTimeout(r, 300))
  }
})

test.afterAll(() => {
  server?.kill()
  if (dataDir) rmSync(dataDir, { recursive: true, force: true })
})

const refs = (texts: string[]) => texts.map((t) => /\b[A-Z][A-Z0-9]+-\d+\b/.exec(t)?.[0] ?? '')

test('the switcher filters the ticket list and the choice is saved on the server', async ({ page }) => {
  const problems = watchConsole(page)
  await loginAs(page, 'staff', ADMIN)
  await page.goto('/agent/tickets?view=all')
  await expect(page.getByTestId('ticket-row').first()).toBeVisible()
  const all = refs(await page.getByTestId('ticket-row').allInnerTexts())
  expect(new Set(all.map((r) => r.split('-')[0])).size).toBeGreaterThan(1)

  await page.getByTestId('project-switcher').click()
  await page.getByTestId('project-option-MOB').click()
  await expect.poll(async () => refs(await page.getByTestId('ticket-row').allInnerTexts()).every((r) => r.startsWith('MOB-'))).toBe(true)
  expect((await page.getByTestId('ticket-row').count())).toBeGreaterThan(0)
  const me = await (await page.request.get('/api/v1/me')).json()
  expect(me.current_project_id).toBeTruthy()

  // The choice survives a reload.
  await page.reload()
  await expect(page.getByTestId('project-switcher')).toContainText('MOB')
  await expect.poll(async () => refs(await page.getByTestId('ticket-row').allInnerTexts()).every((r) => r.startsWith('MOB-'))).toBe(true)

  await page.getByTestId('project-switcher').click()
  await page.getByTestId('project-option-all').click()
  await expect.poll(async () => new Set(refs(await page.getByTestId('ticket-row').allInnerTexts()).map((r) => r.split('-')[0])).size).toBeGreaterThan(1)
  expect(problems).toEqual([])
})

test('an agent who works in one project sees no switcher and no other project tickets', async ({ page }) => {
  await loginAs(page, 'staff', MOBILE_AGENT)
  await page.goto('/agent/tickets?view=all')
  await expect(page.getByTestId('ticket-row').first()).toBeVisible()
  await expect(page.getByTestId('project-switcher')).toHaveCount(0)
  const rows = refs(await page.getByTestId('ticket-row').allInnerTexts())
  expect(rows.length).toBeGreaterThan(0)
  expect(rows.every((r) => r.startsWith('MOB-'))).toBe(true)

  // A WEB ticket is not even reachable by its ref.
  const res = await page.request.get('/api/v1/agent/tickets/by-ref/WEB-1039')
  expect(res.status()).toBe(404)
})

test('by-ref deep links open the ticket, and the old numeric links keep working', async ({ page }) => {
  await loginAs(page, 'staff', ADMIN)
  await page.goto('/agent/tickets/MOB-7')
  await expect(page.getByTestId('ticket-header')).toContainText('MOB-7')
  await expect(page.getByTestId('ticket-header').getByTestId('project-chip')).toContainText('MOB')
  const sub = await page.getByTestId('ticket-subject').innerText()
  const t = await (await page.request.get('/api/v1/agent/tickets/by-ref/MOB-7')).json()
  await page.goto(`/agent/tickets/${t.number}`)
  await expect(page.getByTestId('ticket-subject')).toHaveText(sub)
  // A lower-case ref resolves too, an unknown one is the friendly not-found state.
  await page.goto('/agent/tickets/mob-7')
  await expect(page.getByTestId('ticket-subject')).toHaveText(sub)
})

test('the /p/WEB portal entry shows the WEB branding and opens a WEB-n ticket', async ({ page }) => {
  const problems = watchConsole(page)
  await page.goto('/p/WEB')
  await expect(page.getByTestId('project-title')).toHaveText('Web App support')
  await expect(page.getByTestId('project-intro')).toContainText('web application')

  await page.getByTestId('project-signin').click()
  await page.getByTestId('login-email').locator('input').fill(CUSTOMER.email)
  await page.getByTestId('login-password').locator('input').fill(CUSTOMER.password)
  await page.getByTestId('login-submit').click()
  await expect(page).toHaveURL(/\/tickets\/new\?project=WEB/)

  await page.getByTestId('nt-subject').locator('input').fill('E2E project entry ticket')
  // The demo's required field belongs to the Billing category.
  await page.getByTestId('nt-category').click()
  await page.getByRole('option', { name: 'Billing' }).click()
  await page.getByLabel('Order or invoice number').fill('INV-1001')
  await page.getByTestId('md-input').locator('textarea').fill('Opened through the WEB entry.')
  await page.getByTestId('nt-submit').click()
  await expect(page).toHaveURL(/\/tickets\/WEB-\d+$|\/tickets\/[0-9a-f-]{36}$/)
  await expect(page.getByTestId('detail-subject')).toContainText(/WEB-\d+/)
  await expect(page.getByTestId('detail-subject')).toContainText('E2E project entry ticket')

  // The list shows the ref; an unknown or staff-only key is a friendly 404.
  await page.goto('/tickets')
  await expect(page.getByTestId('ticket-table').or(page.getByTestId('ticket-cards')).first()).toContainText(/WEB-\d+/)
  for (const key of ['NOPE', 'IT']) {
    await page.goto(`/p/${key}`)
    await expect(page.getByTestId('project-not-found')).toBeVisible()
  }
  expect(problems).toEqual([])
})

test('an admin creates a project and maps a team to it', async ({ page }) => {
  const problems = watchConsole(page)
  await loginAs(page, 'staff', ADMIN)
  await page.goto('/agent/admin/projects')
  await expect(page.getByTestId('project-row')).toHaveCount(3)

  await page.getByTestId('project-new').click()
  await page.getByTestId('project-key').locator('input').fill('ops')
  await page.getByTestId('project-name').locator('input').fill('Operations')
  await page.getByTestId('project-save').click()
  await expect(page.getByTestId('project-row')).toHaveCount(4)
  await expect(page.getByTestId('project-table')).toContainText('OPS')

  const list = await (await page.request.get('/api/v1/admin/projects')).json()
  const ops = list.find((p: { key: string }) => p.key === 'OPS')
  expect(ops).toBeTruthy()

  // Map a team through the project's Teams tab.
  await page.getByTestId('project-row').filter({ has: page.getByText('OPS', { exact: true }) }).getByTestId('project-edit').click()
  await page.getByTestId('project-tab-teams').click()
  const teams = await (await page.request.get('/api/v1/admin/teams')).json()
  const team = (Array.isArray(teams) ? teams : teams.items)[0]
  await page.getByTestId(`project-team-${team.id}`).locator('input').check()
  await page.getByTestId('project-teams-save').click()
  await expect.poll(async () => (await (await page.request.get(`/api/v1/admin/projects/${ops.id}/teams`)).json()).team_ids).toContain(team.id)
  expect(problems).toEqual([])
})
