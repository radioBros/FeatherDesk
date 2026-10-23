# Slack

> Part of the [FeatherDesk manual](../DOCUMENTATION.md). The overview is in [Admin guide → Integrations](../DOCUMENTATION.md#722-integrations-channels--integrations-admin); this page has every detail.

FeatherDesk posts ticket activity to Slack channels: new tickets, status and assignment changes, replies, internal notes (optional), SLA alerts and satisfaction ratings. Each ticket gets **one thread** in a channel, so the channel shows one line per ticket and the conversation about it stays together.

- **Your own Slack app.** FeatherDesk is self-hosted, so it posts through a Slack app that you create in your workspace. A button creates it for you with everything filled in; you copy two values back.
- **Connect in a popup.** Slack's permission screen opens in a popup window and closes by itself. The admin page never navigates away.
- **Rules.** Each rule sends the events you choose to one channel, optionally only for some projects or priorities. Add as many rules as you like (for example everything to `#support`, urgent tickets and SLA breaches to `#support-urgent`).
- **Nothing to run.** No extra service, no public endpoint for Slack to call: FeatherDesk only calls Slack.

## 1. Setting it up

*Administration → Channels → Integrations → Slack* (admins).

1. **Create the Slack app.** *Create app in Slack* opens Slack's "create app" page with a pre-filled manifest: the app name (your branding product name), a bot user, the bot permissions below, and this server's redirect URL. Choose your workspace and click *Create*. *Copy manifest* copies the same manifest, for creating the app by hand (*From an app manifest*).
2. **Paste the app credentials.** In the new app, open *Basic Information → App Credentials* and copy the **Client ID** and **Client Secret** into FeatherDesk, then *Save credentials*. The secret is encrypted at rest and never shown again.
3. **Connect your workspace.** *Connect to Slack* opens Slack's permission screen in a popup. Click *Allow*; the popup closes and the page shows the workspace.

If the browser blocks the popup, FeatherDesk says so: allow popups for the site and click again.

**HTTPS is required.** Slack accepts only `https://` redirect URLs, so `FD_PUBLIC_URL` must be an HTTPS address. The setup page warns when it is not. The redirect URL is `<FD_PUBLIC_URL>/api/v1/integrations/slack/oauth/callback`; if you change `FD_PUBLIC_URL`, update it in the Slack app (*OAuth & Permissions → Redirect URLs*).

### Permissions the app asks for

| Scope | Why |
|---|---|
| `chat:write` | Post messages |
| `chat:write.public` | Post to public channels without being invited |
| `channels:read` | List public channels in the channel picker |
| `groups:read` | List private channels the bot was invited to |

The app cannot read messages. Organisation-wide (Enterprise Grid) installs are not supported; install it in a workspace.

## 2. Rules

*Add rule* (or *Post new tickets to a channel* on an empty list) opens the rule editor, with a live preview of the posts on the right.

- **Channel.** Public channels work right away. A private channel works once the bot is in it: in Slack, type `/invite @<bot name>` in the channel. The picker marks private channels that still need the invite, and *Reload channels* refreshes the list (it is cached for a minute).
- **What to post.** Presets (*New tickets*, *Ticket updates*, *Conversation*, *SLA alerts*, *Everything*) or individual events:

| Event | Posted when |
|---|---|
| New ticket | A ticket is created (with the first message quoted) |
| Status changed | The status changes |
| Assigned | The assignee changes |
| Priority changed | The priority changes |
| Escalated or de-escalated | The support level changes |
| Customer replied | A customer (or an integration on their behalf) replies |
| Agent replied | A staff member replies to the customer |
| Internal note | A staff note is added — needs *Include internal notes* |
| SLA about to breach / SLA breached | The first-response or resolution SLA warns or breaches |
| Satisfaction rating | The customer rates the ticket |

- **Filters.** Projects and priorities; none selected means all.
- **Options.**
  - *Post updates in the thread of the ticket's first message* (on by default): the first post about a ticket in the channel becomes its thread, later events reply in it. SLA breaches and escalations are also shown in the channel.
  - *Include the message text*: quotes the first 600 characters of the message.
  - *Include internal notes and staff-only details*: only for channels where everyone is staff. Without it, internal notes, escalation reasons and staff-only custom fields never reach Slack (the same rules as webhooks without *Include internal notes*).
  - *Active*: pause a rule without deleting it (the switch on its card too).
- **Send a test message** posts a short message to the channel and shows Slack's answer.

Posts are in English: a channel is shared by people who use FeatherDesk in different languages.

## 3. When something goes wrong

- **Channel problems** (the bot is not in a private channel, the channel was archived or deleted) appear on the rule's card with what to do. FeatherDesk does not retry those posts; it tries again with the next event.
- **Slack is slow or rate-limits** (`ratelimited`): the post is retried in the background with back-off, honouring Slack's `Retry-After`, up to 8 attempts (*System → Failed jobs* lists the ones that gave up).
- **The app was removed or its token revoked** (`token_revoked`, `invalid_auth`, `account_inactive`): the page shows *Slack rejected the connection* and nothing is posted until you *Reconnect*. Events in between are not posted later.
- **Disconnect** revokes the token. The app credentials and the rules stay, so *Connect* resumes where you left off. Connecting a different workspace forgets the old ticket threads.

Every change is in the audit log: `slack.app_updated`, `slack.connected`, `slack.disconnected`, `slack.route_created`, `slack.route_updated`, `slack.route_deleted`, `slack.route_tested`.

## 4. API

All under `/api/v1`, admin only (a token needs the `admin` scope):

| Method and path | What it does |
|---|---|
| `GET /admin/integrations/slack` | Status: credentials set, workspace, bot, scopes, error, redirect URL, manifest and manifest URL, rule counts |
| `PATCH /admin/integrations/slack` | Save `client_id` and `client_secret` (write-only) |
| `POST /admin/integrations/slack/oauth/start` | Consent URL and single-use `state` for the popup (10 minutes) |
| `GET /integrations/slack/oauth/callback` | Slack's redirect target; answers 302 to the popup's completion page |
| `DELETE /admin/integrations/slack/connection` | Disconnect (revokes the token) |
| `GET /admin/integrations/slack/channels` | Channel picker data (`refresh=true` reloads) |
| `GET`/`POST /admin/integrations/slack/routes`, `PATCH`/`DELETE …/routes/{id}` | Rules |
| `POST /admin/integrations/slack/routes/{id}/test` | Post a test message |

## 5. How it works

- The settings key `slack` holds the client ID, the **encrypted** client secret and bot token, and the workspace details. `featherdesk rotate-secrets` re-encrypts both secrets. Rules are the table `slack_routes`; `slack_threads` remembers each ticket's thread per rule.
- Delivery runs on the built-in queue as the durable consumer `slack` with **one worker**, so a ticket's updates never overtake its first post. The first attempt happens in memory; failures become `slack.post` jobs in `deferred_jobs`. Posts are built from the same event envelope as webhooks, so visibility rules are shared.
- **The popup flow.** `POST …/oauth/start` returns Slack's consent URL with a random single-use `state` bound to the admin. Slack sends the popup to the callback, which is authenticated by that `state` only (not by the session cookie), exchanges the code for the bot token and redirects the popup to `/agent/oauth/complete`. That page tells the admin page with `window.opener.postMessage` and over a same-origin `BroadcastChannel` — FeatherDesk sends `Cross-Origin-Opener-Policy: same-origin`, so after the provider's pages the browser cuts the link to the opener and only the channel still connects the two windows — and closes itself. The same pieces serve future OAuth integrations (`internal/integrations/oauthflow`, `web/src/areas/agent/integrations/oauth`).
- `FD_SLACK_BASE_URL` points FeatherDesk at another Slack origin. It exists for tests (the end-to-end suite runs a fake Slack); leave it unset.
