// SPDX-License-Identifier: AGPL-3.0-only

import { defineConfig, devices } from '@playwright/test'
import { fileURLToPath } from 'node:url'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

/**
 * E2E against the real Go binary (SPA embedded). With E2E_BASE_URL set (e.g. a
 * docker compose stack) no server is started; otherwise a fresh instance boots
 * on a temp SQLite DB with a bootstrap admin:
 *   E2E_SERVER_BIN     run this built binary (e.g. ../bin/featherdesk from
 *                      `make build`) instead of `go run ./cmd/featherdesk`
 *   E2E_DATABASE_URL   use this database instead of the temp SQLite file
 *                      (e.g. an empty Postgres database)
 *   E2E_MAILPIT_URL    Mailpit API; the server then sends SMTP to
 *                      E2E_SMTP_HOST:E2E_SMTP_PORT (default 127.0.0.1:1025)
 */
const external = process.env.E2E_BASE_URL
const port = Number(process.env.E2E_PORT ?? 18080)
const baseURL = external ?? `http://127.0.0.1:${port}`
const repoRoot = fileURLToPath(new URL('..', import.meta.url))
const dataDir = external ? '' : mkdtempSync(join(tmpdir(), 'fd-e2e-'))
const serverBin = process.env.E2E_SERVER_BIN || ''
const smtp: Record<string, string> = process.env.E2E_MAILPIT_URL?.trim()
  ? {
      FD_SMTP_HOST: process.env.E2E_SMTP_HOST ?? '127.0.0.1',
      FD_SMTP_PORT: process.env.E2E_SMTP_PORT ?? '1025',
      FD_SMTP_SECURITY: 'none',
      FD_SMTP_AUTH: 'none',
      FD_SMTP_FROM_CUSTOMER: 'E2E Support <support@e2e.test>',
      FD_SMTP_FROM_STAFF: 'E2E Help desk <helpdesk@e2e.test>',
    }
  : {}

/** Specs that change instance-wide settings and so must not run beside others. */
const ISOLATED = /portal-toggle\.spec\.ts$/

const E2E_ADMIN = {
  email: process.env.E2E_ADMIN_EMAIL ?? 'admin@e2e.test',
  password: process.env.E2E_ADMIN_PASSWORD ?? 'e2e-admin-password-123',
}

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  // Authentik (Python, several round trips per login) on a shared CI runner is ~4x slower than a
  // workstation: the SSO suite (E2E_AUTHENTIK_URL) gets longer expect/test budgets, not weaker assertions.
  expect: { timeout: process.env.E2E_AUTHENTIK_URL ? 20_000 : 7_000 },
  fullyParallel: false,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    { name: 'chromium', testIgnore: ISOLATED, use: { ...devices['Desktop Chrome'] } },
    { name: 'mobile', testIgnore: ISOLATED, use: { ...devices['Pixel 7'] } },
    // Flips an instance-wide setting (support.portal_enabled): runs alone, after the other projects,
    // so no spec running in parallel sees the customer portal switched off.
    { name: 'portal-toggle', testMatch: ISOLATED, dependencies: ['chromium', 'mobile'], use: { ...devices['Desktop Chrome'] } },
  ],
  webServer: external
    ? undefined
    : {
        command: serverBin ? `${serverBin} serve` : 'go run ./cmd/featherdesk serve',
        cwd: repoRoot,
        url: `${baseURL}/readyz`,
        timeout: 180_000,
        reuseExistingServer: !process.env.CI,
        env: {
          FD_LISTEN_ADDR: `127.0.0.1:${port}`,
          FD_PUBLIC_URL: baseURL,
          FD_DATA_DIR: dataDir,
          FD_DATABASE_URL: process.env.E2E_DATABASE_URL || `sqlite://${join(dataDir, 'e2e.db')}`,
          FD_SECRET_KEY: 'e2e-secret-key-0123456789abcdef0123456789abcdef',
          FD_BOOTSTRAP_ADMIN_EMAIL: E2E_ADMIN.email,
          FD_BOOTSTRAP_ADMIN_PASSWORD: E2E_ADMIN.password,
          FD_AUTH_STAFF_LOCAL: 'true',
          // e2e/fixtures.ts gives every test its own X-Forwarded-For client IP.
          FD_TRUSTED_PROXIES: '127.0.0.1/32,::1/128',
          FD_LOG_LEVEL: process.env.E2E_LOG_LEVEL ?? 'warn',
          ...smtp,
        },
      },
})
