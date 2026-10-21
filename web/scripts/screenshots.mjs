// SPDX-License-Identifier: AGPL-3.0-only
//
// DEP-027: regenerates the README screenshots with no manual steps.
//
//   pnpm screenshots
//
// 1. builds the SPA and the binary (or uses SCREENSHOTS_SERVER_BIN),
// 2. seeds a throwaway SQLite database with demo data (`seed --demo` when the
//    binary has it, else `seed --tickets 40`) and starts `serve` on it,
// 3. signs in as an admin and as a seeded customer (password set with the
//    `reset-password` CLI) and captures the customer portal, the agent ticket
//    list, the branding, automation and privacy admin pages, the portal
//    notification centre and two-step page in the light and the dark theme,
// 4. writes docs/screenshots/<view>-<theme>.png.
//
// Env: SCREENSHOTS_SERVER_BIN (prebuilt binary; skips both builds),
// SCREENSHOTS_PORT (default 18220), SCREENSHOTS_OUT (default ../docs/screenshots).
import { chromium } from '@playwright/test'
import { execFileSync, spawn, spawnSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const web = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const root = resolve(web, '..')
const out = resolve(web, process.env.SCREENSHOTS_OUT ?? '../docs/screenshots')
const port = Number(process.env.SCREENSHOTS_PORT ?? 18220)
const base = `http://127.0.0.1:${port}`
const work = mkdtempSync(join(tmpdir(), 'st-screenshots-'))
const admin = { email: 'admin@screenshots.test', password: 'screenshots-admin-password-123' }

const env = {
  ...process.env,
  ST_LISTEN_ADDR: `127.0.0.1:${port}`,
  ST_PUBLIC_URL: base,
  ST_DATA_DIR: join(work, 'data'),
  ST_SECRET_KEY: 'screenshots-secret-key-0123456789abcdef0123456789',
  ST_AUTH_STAFF_LOCAL: 'true',
  ST_LOG_LEVEL: 'warn',
}
mkdirSync(env.ST_DATA_DIR, { recursive: true })
const run = (cmd, args, opts = {}) => execFileSync(cmd, args, { stdio: ['ignore', 'pipe', 'inherit'], encoding: 'utf8', ...opts })

let bin = process.env.SCREENSHOTS_SERVER_BIN && resolve(process.env.SCREENSHOTS_SERVER_BIN)
if (!bin) {
  console.log('building the SPA and the binary …')
  run('pnpm', ['build'], { cwd: web, stdio: 'inherit' })
  bin = join(work, 'simpletickets')
  run('go', ['build', '-o', bin, './cmd/simpletickets'], { cwd: root, env: { ...process.env, CGO_ENABLED: '0' }, stdio: 'inherit' })
}

// Demo data before the server starts (seed migrates the empty database).
// `seed --help` prints the flag usage on stderr.
const help = spawnSync(bin, ['seed', '--help'], { env, encoding: 'utf8' })
const seedHelp = `${help.stdout ?? ''}${help.stderr ?? ''}`
const seedArgs = seedHelp.includes('-demo') ? ['seed', '--demo'] : ['seed', '--tickets', '40']
console.log(`seeding: simpletickets ${seedArgs.join(' ')}`)
run(bin, seedArgs, { env, stdio: ['ignore', 'ignore', 'inherit'] })
// The seed fills `users`, so the env bootstrap would be a no-op: use the CLI.
execFileSync(bin, ['create-admin', '--email', admin.email, '--name', 'Screenshot Admin', '--password-stdin'], {
  env,
  input: `${admin.password}\n`,
  stdio: ['pipe', 'ignore', 'inherit'],
})

const server = spawn(bin, ['serve'], { env, stdio: ['ignore', 'ignore', 'inherit'] })
const stop = () => {
  server.kill('SIGTERM')
  rmSync(work, { recursive: true, force: true })
}
process.on('exit', stop)

async function ready() {
  for (let i = 0; i < 200; i++) {
    try {
      if ((await fetch(`${base}/readyz`)).ok) return
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 100))
  }
  throw new Error('server not ready')
}

const views = {
  staff: [
    ['agent-tickets', '/agent/tickets'],
    ['branding', '/agent/admin/branding'],
    ['automation', '/agent/admin/automation'],
    ['privacy', '/agent/admin/privacy'],
  ],
  customer: [
    ['portal', '/tickets'],
    ['notifications', '/notifications'],
    ['security', '/profile/security'],
  ],
}

async function main() {
  await ready()
  const browser = await chromium.launch()
  try {
    // A seeded customer with tickets: the requester of the newest ticket.
    const probe = await browser.newContext({ baseURL: base })
    const login = await probe.request.post('/api/v1/auth/login', { data: { audience: 'staff', ...admin } })
    if (!login.ok()) throw new Error(`admin login: ${login.status()} ${await login.text()}`)
    const list = await (await probe.request.get('/api/v1/agent/tickets?limit=1')).json()
    const requesterId = list.items?.[0]?.requester?.id
    if (!requesterId) throw new Error('the seed created no tickets')
    const customer = await (await probe.request.get(`/api/v1/agent/customers/${requesterId}`)).json()
    await probe.close()
    const reset = execFileSync(bin, ['reset-password', '--email', customer.email], { env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
    const password = reset.match(/[A-Za-z0-9_-]{16,}/g)?.pop()
    if (!password) throw new Error(`reset-password printed no password: ${reset}`)
    const accounts = { staff: { audience: 'staff', ...admin }, customer: { audience: 'customer', email: customer.email, password } }

    mkdirSync(out, { recursive: true })
    for (const theme of ['light', 'dark']) {
      for (const [who, pages] of Object.entries(views)) {
        const ctx = await browser.newContext({ baseURL: base, viewport: { width: 1440, height: 900 }, colorScheme: theme, locale: 'en-US', deviceScaleFactor: 1 })
        await ctx.addInitScript((t) => {
          localStorage.setItem('st.theme', t)
          localStorage.setItem('st.locale', 'en')
        }, theme)
        const res = await ctx.request.post('/api/v1/auth/login', { data: accounts[who] })
        if (!res.ok()) throw new Error(`${who} login: ${res.status()} ${await res.text()}`)
        const page = await ctx.newPage()
        for (const [name, path] of pages) {
          await page.goto(path)
          await page.waitForLoadState('networkidle')
          await page.locator('main').first().waitFor({ state: 'visible' })
          await page.evaluate(() => document.fonts.ready)
          await page.waitForTimeout(300) // let transitions settle
          const file = join(out, `${name}-${theme}.png`)
          await page.screenshot({ path: file })
          console.log(`wrote ${file}`)
        }
        await ctx.close()
      }
    }
  } finally {
    await browser.close()
  }
}

main().then(
  () => process.exit(0),
  (err) => {
    console.error(err)
    process.exit(1)
  },
)
