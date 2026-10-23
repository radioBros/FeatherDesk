// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Fake Slack for the integrations E2E (D-21, INT-018). A second origin, so the
 * OAuth popup really leaves the app (and COOP severs window.opener) before
 * coming back. Serves:
 *   GET  /oauth/v2/authorize   a consent page with an "Allow" link
 *   POST /api/<method>         oauth.v2.access, auth.test, auth.revoke,
 *                              conversations.list, chat.postMessage
 *   GET  /e2e/posts            the recorded chat.postMessage bodies
 *   GET  /health
 * Run: node e2e/fake-slack.mjs [port]   (Playwright starts it, see playwright.config.ts)
 */
import { createServer } from 'node:http'

const port = Number(process.argv[2] ?? process.env.FAKE_SLACK_PORT ?? 18090)
const posts = []
let ts = 0

const body = (req) =>
  new Promise((resolve) => {
    let b = ''
    req.on('data', (c) => (b += c))
    req.on('end', () => resolve(b))
  })
const send = (res, status, obj, type = 'application/json') => {
  res.writeHead(status, { 'Content-Type': type })
  res.end(typeof obj === 'string' ? obj : JSON.stringify(obj))
}
const esc = (s) => s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`)

createServer(async (req, res) => {
  const url = new URL(req.url, `http://127.0.0.1:${port}`)
  if (url.pathname === '/health') return send(res, 200, { ok: true })
  if (url.pathname === '/e2e/posts') return send(res, 200, posts)
  if (url.pathname === '/oauth/v2/authorize') {
    const back = new URL(url.searchParams.get('redirect_uri'))
    back.searchParams.set('code', 'e2e-code')
    back.searchParams.set('state', url.searchParams.get('state') ?? '')
    const deny = new URL(url.searchParams.get('redirect_uri'))
    deny.searchParams.set('error', 'access_denied')
    deny.searchParams.set('state', url.searchParams.get('state') ?? '')
    return send(
      res,
      200,
      `<!doctype html><title>Fake Slack</title><h1>E2E Workspace</h1><p>Scopes: ${esc(url.searchParams.get('scope') ?? '')}</p>` +
        `<a id="allow" href="${esc(back.toString())}">Allow</a> <a id="deny" href="${esc(deny.toString())}">Cancel</a>`,
      'text/html',
    )
  }
  const method = url.pathname.replace(/^\/api\//, '')
  const raw = await body(req)
  switch (method) {
    case 'oauth.v2.access': {
      const f = new URLSearchParams(raw)
      if (f.get('code') !== 'e2e-code' || f.get('client_secret') !== 'e2e-secret') return send(res, 200, { ok: false, error: 'invalid_code' })
      return send(res, 200, {
        ok: true, access_token: 'xoxb-e2e', token_type: 'bot', scope: 'chat:write,chat:write.public,channels:read,groups:read',
        bot_user_id: 'UBOT', app_id: 'AAPP', team: { id: 'TE2E', name: 'E2E Workspace' }, enterprise: null, is_enterprise_install: false,
      })
    }
    case 'auth.test':
      return send(res, 200, { ok: true, url: 'https://e2e.slack.test/', team: 'E2E Workspace', user: 'helpdesk', team_id: 'TE2E', user_id: 'UBOT' })
    case 'auth.revoke':
      return send(res, 200, { ok: true, revoked: true })
    case 'conversations.list':
      return send(res, 200, {
        ok: true,
        channels: [
          { id: 'C0SUPPORT', name: 'support', is_private: false, is_member: false, num_members: 12 },
          { id: 'G0ALERTS', name: 'alerts', is_private: true, is_member: true, num_members: 3 },
        ],
        response_metadata: { next_cursor: '' },
      })
    case 'chat.postMessage': {
      if (req.headers.authorization !== 'Bearer xoxb-e2e') return send(res, 200, { ok: false, error: 'invalid_auth' })
      const m = JSON.parse(raw || '{}')
      posts.push(m)
      ts += 1
      return send(res, 200, { ok: true, channel: m.channel, ts: `1700000000.${String(ts).padStart(6, '0')}` })
    }
    default:
      return send(res, 404, { ok: false, error: 'unknown_method' })
  }
}).listen(port, '127.0.0.1')
