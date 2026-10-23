<p align="center"><img src="docs/images/featherdesk-logo.svg" alt="FeatherDesk logo" height="120"></p>

# FeatherDesk

**A self-hosted help desk that runs as one small container.**

FeatherDesk is an open-source help desk with a customer portal, an agent panel, a knowledge base, email in and out, and a complete REST and WebSocket API. Everything runs in one process: database, API, realtime hub, job queue, scheduler and web app. It runs comfortably on a 1 vCPU / 512 MB host and fits in 256 MB.

FeatherDesk is developed by [RadioBros](https://radiobros.com/).

**Version 1.1.1** (2026-10-23): see the [changelog](CHANGELOG.md).

SPDX-License-Identifier: AGPL-3.0-only

- **Full manual:** [docs/DOCUMENTATION.md](docs/DOCUMENTATION.md)
- **Configuration reference:** [docs/configuration.md](docs/configuration.md)
- **API specification:** [api/openapi.yaml](api/openapi.yaml). A running instance also serves it at `/api/v1/openapi.yaml`, with a browsable reference at `/api/docs`.

---

## Contents

- [Features](#features)
- [Screenshots](#screenshots)
- [Architecture](#architecture)
- [Footprint](#footprint)
- [Quick start](#quick-start)
- [First-run setup wizard](#first-run-setup-wizard)
- [Configuration essentials](#configuration-essentials)
- [Single sign-on with Authentik](#single-sign-on-with-authentik)
- [Roles and support levels](#roles-and-support-levels)
- [Projects](#projects)
- [Automation and assignment](#automation-and-assignment)
- [Two-step verification](#two-step-verification)
- [Privacy and GDPR](#privacy-and-gdpr)
- [Notification centre](#notification-centre)
- [AI assistants (MCP)](#ai-assistants-mcp)
- [Slack](#slack)
- [API and WebSocket](#api-and-websocket)
- [Operations at a glance](#operations-at-a-glance)
- [Development](#development)
- [Demo data](#demo-data)
- [Testing](#testing)
- [Documentation map](#documentation-map)
- [Licence](#licence)

---

## Features

**For customers (the portal at `/`)**
- An admin can switch the portal off (**Administration → System → Support settings → Customer portal enabled**, setting `support.portal_enabled`, on by default): then only admins and staff sign in. Customers still get emails and can reply by email; the public help center follows its own setting. See the [documentation](docs/DOCUMENTATION.md#switching-the-customer-portal-off).
- Customers open tickets, follow the conversation, reply, attach files, mark a ticket solved or reopen it.
- They can sign in with a password, a magic link sent by email, or single sign-on. Self-registration with email verification can be switched on or off.
- Organization managers see every ticket of their organization, open tickets for colleagues and invite or disable members.
- A satisfaction rating (1 to 5) is offered when a ticket is resolved. The rating link in the email works without signing in.
- The help center (knowledge base) has search. While a customer types a new ticket subject, matching articles are suggested.
- A **notification bell** shows new replies and resolved requests live, with optional browser pop-ups.
- **Two-step verification** with an authenticator app, codes by email and recovery codes.
- **My data:** customers download an export of their personal data and can ask for their account to be deleted.

**For support staff (the agent panel at `/agent`)**
- **Projects:** a top-bar switcher limits lists, counts and reports to one project; tickets show `KEY-123` references and project chips and open by reference (`/agent/tickets/WEB-123`); a ticket can be moved to another project.
- Queues update live: My tickets, Unassigned, Triage, All tickets, one queue per tier, and saved views (private or shared).
- Filters cover status, priority, tier, team, assignee, category, tag, breached SLA and custom fields. A "needs attention" sort is the default. Bulk actions are available.
- Replies and **internal notes** are written in Markdown, with attachments, canned responses, knowledge-base links and @mentions in notes. Agents can send and set the status in one step.
- **Escalate and de-escalate** between three support tiers, with a required reason.
- Tickets can be assigned, transferred between teams, watched, linked (parent, related, duplicate), merged and split.
- Presence shows who else is viewing or replying to a ticket.
- Business-hours **SLA** with first-response and resolution targets, warnings at 80 % and breach alerts.
- Customer and organization directory, reports, a personal email signature, and keyboard shortcuts.
- **Macros** apply a reply, status, priority, tags and assignee in one click, personal or shared.
- A **notification centre** (bell, full page, browser pop-ups) with per-type preferences for in-app, email and browser.
- An **Away** switch pauses automatic assignment while you are out.

**For administrators**
- A first-run **setup wizard**, protected by a one-time setup token.
- **White-label branding:** logos, colours, fonts, theme, login page, portal texts, email look, legal links and custom CSS.
- **SMTP configured in the UI.** Senders for customers and for staff, team mailboxes, a triage mailbox, a switch for each notification, previews and a test button.
- **Inbound email:** IMAP mailboxes (IMAP IDLE, password or XOAUTH2) and a raw-MIME webhook for mail providers. Replies are threaded, mail loops are blocked, and suspicious mail is quarantined.
- **SSO** with Authentik or any OpenID Connect provider, configured separately for staff and for customers. Group membership sets role, tier and team on every login. Back-channel logout is supported.
- Teams, categories, custom fields (7 types), SLA policies, business-hours calendars, canned responses, tags and knowledge-base categories.
- Signed outgoing **webhooks** for 21 event types, with a delivery log and automatic disabling.
- **Projects:** group tickets per product, client or service, each with its own key and references (`WEB-12`), categories, teams, SLA overrides, mailboxes, members and portal branding. Agents only see their projects.
- **Automation:** per-team automatic assignment (round robin or least busy) and rules that route, tag, reply, escalate or close tickets on events or on a schedule, with a dry run.
- **Two-step verification policy** for local accounts (optional, all staff, or everyone), with admin and CLI resets.
- **Privacy (GDPR) tools:** data exports, erasure, deletion requests, retention policies and consent records.
- Personal access tokens with scopes, an audit log, a queue and failed-jobs view, and a system status page.

**Platform**
- One container, with a static Go binary and the web app embedded in it. There is no Redis and no message broker.
- **SQLite** by default. **PostgreSQL** is optional.
- Attachments are stored on local disk by default. **S3-compatible storage** is optional.
- UI and emails in **English, Italian, German, French and Spanish**.
- Prometheus metrics, health and readiness probes, online backups, and migrations that run automatically.
- Docker Compose, plain Kubernetes manifests and a Helm chart.

**Interface**
- **Column toggles:** every table, including the ticket list, has a **Columns** button to show or hide columns, remembered per user and per table in the browser. The reference and subject columns always stay; less important columns start hidden on a phone.
- **One compact density** for every field (40 px outlined inputs, selects and dates) and comfortable rows in the ticket list. It is fixed, not a setting.
- **Password eye toggle:** every password field has an eye button to show or hide the password; it hides again when the field loses focus.
- **A colourful theme:** each section has its own hue (tickets blue, knowledge base teal, reports purple, automation amber, notifications orange, privacy green, projects brown, …) on icons, page headers, chips and avatars, with status and priority chips in distinct colours. Light and dark variants are contrast-checked (WCAG AA); the brand colour stays the colour of primary actions.
- Light, dark and system themes, keyboard shortcuts, and a layout that works on phones.

## Screenshots

| | Light | Dark |
|---|---|---|
| Customer portal | ![Customer portal, light theme](docs/screenshots/portal-light.png) | ![Customer portal, dark theme](docs/screenshots/portal-dark.png) |
| Agent panel | ![Agent ticket list, light theme](docs/screenshots/agent-tickets-light.png) | ![Agent ticket list, dark theme](docs/screenshots/agent-tickets-dark.png) |
| Project switcher | ![Agent top-bar project switcher, light theme](docs/screenshots/agent-project-switcher-light.png) | ![Agent top-bar project switcher, dark theme](docs/screenshots/agent-project-switcher-dark.png) |
| Projects admin | ![Projects admin page, light theme](docs/screenshots/projects-light.png) | ![Projects admin page, dark theme](docs/screenshots/projects-dark.png) |
| Branding settings | ![Branding page, light theme](docs/screenshots/branding-light.png) | ![Branding page, dark theme](docs/screenshots/branding-dark.png) |
| Automation rules | ![Automation rules, light theme](docs/screenshots/automation-light.png) | ![Automation rules, dark theme](docs/screenshots/automation-dark.png) |
| Privacy requests | ![Privacy requests admin page, light theme](docs/screenshots/privacy-light.png) | ![Privacy requests admin page, dark theme](docs/screenshots/privacy-dark.png) |
| Notification centre | ![Portal notification centre, light theme](docs/screenshots/notifications-light.png) | ![Portal notification centre, dark theme](docs/screenshots/notifications-dark.png) |
| Two-step verification | ![Portal security page, light theme](docs/screenshots/security-light.png) | ![Portal security page, dark theme](docs/screenshots/security-dark.png) |

The screenshots are generated from demo data (`featherdesk seed --demo`) by `cd web && pnpm screenshots` (Playwright; the nightly CI job `screenshots` does the same).

## Architecture

```
  Customers / staff           Scripts & integrations     Identity provider     Mail server
  (browser SPA)               (PAT, WebSocket)           (Authentik / OIDC)    (SMTP / IMAP)
        │  HTTPS + WSS               │                         │                    │
  ┌─────▼────────────────────────────▼─────────────────────────▼────────────────────▼───┐
  │ Reverse proxy (TLS)  — Caddy / Traefik / nginx / Ingress                            │
  └─────┬───────────────────────────────────────────────────────────────────────────────┘
        │ :8080
  ┌─────▼──────────────── featherdesk (one container, one process) ───────────────────┐
  │  HTTP router ── REST /api/v1 ── WebSocket hub /api/v1/ws ── embedded web app (SPA)   │
  │                 /healthz  /readyz  /metrics                                         │
  │  Modules: auth · tickets · directory · knowledge base · notify · webhooks · mailin   │
  │           automation · privacy · in-app notifications                                │
  │  In-process queue: ticket_events (outbox) → realtime · email · webhooks · search     │
  │                      → automation rules · in-app notifications                       │
  │  Scheduler: SLA checks (1 min), auto-close (1 h), pruning, write-behind flushes      │
  │             automation time rules (1 h), retention policy (24 h)                     │
  └─────┬───────────────────────────────────────┬───────────────────────────────────────┘
        │                                       │
  SQLite in /data  (or PostgreSQL)        Attachments in /data  (or S3-compatible)
```

Every change to a ticket writes one row to the `ticket_events` table, in the same transaction as the change. After the commit, an in-process dispatcher reads those rows in order and fans them out:
- WebSocket subscribers get a live update, after an in-memory visibility check per subscriber.
- Email notifications and webhooks are sent.
- The search index is updated.

Failed deliveries are retried from the database, so nothing is lost on a restart. Because the queue lives in memory, FeatherDesk runs as **exactly one replica**. It scales vertically.

## Footprint

Measured on 2026-10-21 with the production compose file (one container, read-only, `mem_limit: 256m`, `cpus: 1`) on Docker Desktop for Apple Silicon. The `image` CI job re-measures size, idle memory, idle CPU and cold start on every change with `scripts/footprint.sh` and fails above the hard limit; the nightly load test runs `scripts/loadtest.sh`.

| | Measured | Target | Hard limit |
|---|---|---|---|
| Containers | 1 | 1 | 1 |
| Image size (compressed) | 11.1 MiB (arm64, D-19 build); earlier 10.5 (arm64) / 11.3 (amd64) | ≤ 20 MB | ≤ 30 MB |
| Memory at idle (RSS, 1 000 tickets) | 26.5 MiB (D-19 build; was ~21 MiB before it) | ≤ 35 MB | ≤ 50 MB |
| Memory under load (50 agents, 200 WebSockets, 10 req/s, 10 min) | 42 MiB peak | ≤ 80 MB | ≤ 120 MB |
| CPU at idle | 0.03 % | ≈ 0 % | < 0.10 % of the app process on Linux (CI); < 1 % in `docker stats` elsewhere |
| Cold start (container start → ready) | 0.11 s (median) | ≤ 1 s | ≤ 3 s |
| Initial JavaScript per area (gzip) | 190–200 KB (portal 193.7, agent 199.7, kb 193.3, setup 190.4) | ≤ 200 KB | ≤ 300 KB |
| Optional Postgres container at idle | 42–51 MiB | ≤ 60 MB | — |

The reference host is 1 vCPU with 512 MB of RAM, such as a Raspberry Pi 4. The app also runs with a 256 MB container limit. When `GOMEMLIMIT` is not set, the binary sets it to 75 % of the container memory limit.

## Quick start

You need Docker Engine 25 or newer with the Compose plugin v2.24.4 or newer, and a DNS name for your help desk if you want to reach it from other machines.

```sh
git clone https://github.com/radioBros/FeatherDesk.git && cd FeatherDesk
cp .env.example deploy/.env    # then set FD_SECRET_KEY (openssl rand -base64 32) and FD_PUBLIC_URL
docker build -t ghcr.io/radiobros/featherdesk:latest .   # no image is published on ghcr.io yet: build it from this checkout
docker compose -f deploy/compose.yml up -d
```

Then read the setup token from the logs and open `/setup`:

```sh
docker compose -f deploy/compose.yml logs app | grep "setup token"
```

- The shipped compose file publishes port 8080 on `127.0.0.1` only. Put a TLS reverse proxy in front of it; there are examples for Caddy, Traefik and nginx in `deploy/examples/`.
- To try FeatherDesk on your own machine, set `FD_PUBLIC_URL=http://localhost:8080` and open http://localhost:8080/setup.
- `FD_PUBLIC_URL` must be exactly the address people type into the browser. The API compares the browser's `Origin` with it, and links in emails, SSO redirect URIs and the WebSocket check all depend on it.
- **Any host port:** the container listens on 8080; publish it wherever you like. With Compose set `FD_PORT=3200` (and `FD_BIND=0.0.0.0`) in `deploy/.env`; with plain Docker:

  ```sh
  docker run -d -p 3200:8080 -v fd-data:/data -e FD_SECRET_KEY="$FD_SECRET_KEY" \
    -e FD_PUBLIC_URL=http://192.168.1.50:3200 \
    -e FD_ALLOWED_ORIGINS=http://localhost:3200,http://127.0.0.1:3200 \
    ghcr.io/radiobros/featherdesk:latest
  ```

  `FD_PUBLIC_URL` includes the port. `FD_ALLOWED_ORIGINS` lists the other addresses of the same instance that browsers use (comma-separated `scheme://host[:port]`); without it, sign-in and saves from them fail with 403 `csrf`. See [Another host port or address](docs/DOCUMENTATION.md#another-host-port-or-address).

For PostgreSQL, S3, Kubernetes, Helm and reverse proxies, see [Installation](docs/DOCUMENTATION.md#2-installation).

## First-run setup wizard

On an empty database, the server prints a one-time **setup token** to its log at WARN level:

```
setup token: Xy9…QA — open https://help.example.com/setup to create the first admin
```

Open `/setup`. The wizard has six steps:

1. **Setup token:** paste the token from the log.
2. **Administrator:** your name, email and a password of at least 10 characters.
3. **Branding:** product name, company name, primary colour and an optional logo. "Create the help desk" creates the admin and signs you in.
4. **Email:** SMTP server and sender, with a test email. You can skip this step.
5. **Single sign-on:** shows the redirect URIs to paste into your identity provider.
6. **Done:** opens the agent panel.

Some details about the token and the wizard:
- The token is kept only in memory, and a new one is printed on every restart until an admin exists.
- After the first admin is created, `/setup` disappears.
- The administrator created here is a **break-glass** account: it can always sign in with its password, even when staff SSO is enforced.

For a headless install, skip the wizard with `FD_BOOTSTRAP_ADMIN_EMAIL` and `FD_BOOTSTRAP_ADMIN_PASSWORD`, or run:

```sh
docker compose -f deploy/compose.yml exec app /featherdesk create-admin --email admin@example.com
```

## Configuration essentials

FeatherDesk is configured with `FD_*` environment variables. Branding, SMTP, inbound mailboxes and support policies are also edited in the admin UI.

- If an `FD_SMTP_*` or `FD_IMAP_*` variable is set, it wins over the value stored in the database, and the admin UI shows that field as "Set by environment".
- All configuration errors are reported together at startup.
- Run `featherdesk config check` to validate the configuration without starting the server.
- Projects, automation, two-step verification, privacy, the notification centre, the customer portal switch (`support.portal_enabled`) and the MCP switch (`integrations.mcp_enabled`) need no variables: they are configured in the admin UI ([settings list](docs/DOCUMENTATION.md#settings-stored-in-the-database)).

| Variable | Default | What it does |
|---|---|---|
| `FD_SECRET_KEY` | — (required) | Root secret, at least 32 bytes. It encrypts SMTP, IMAP and webhook secrets. Keep it safe and back it up |
| `FD_PUBLIC_URL` | `http://localhost:8080` | Public base URL. Used in links, OIDC redirect URIs and the Origin/WebSocket checks |
| `FD_ALLOWED_ORIGINS` | — | Other browser origins of the same instance (comma-separated `scheme://host[:port]`, e.g. `http://localhost:3200`), accepted by the Origin and WebSocket checks besides `FD_PUBLIC_URL` |
| `FD_DATA_DIR` | `/data` | SQLite database, attachments, backups and the instance lock |
| `FD_DATABASE_URL` | SQLite in `FD_DATA_DIR` | Set `postgres://…` to use PostgreSQL |
| `FD_BLOB_URL` | local disk | Set `s3://bucket/prefix?endpoint=…&region=…` (plus `FD_S3_ACCESS_KEY` / `FD_S3_SECRET_KEY`) to use S3 |
| `FD_TRUSTED_PROXIES` | — | CIDRs of your reverse proxy, so client IPs come from `X-Forwarded-For` |
| `FD_AUTH_STAFF_LOCAL` | `false` | Allow staff password login. Off by default, because staff normally use SSO. Break-glass admins can always use a password |
| `FD_SMTP_HOST`, `FD_SMTP_*` | — | Optional. Sets SMTP from the environment instead of the UI |
| `FD_OIDC_STAFF_*`, `FD_OIDC_CUSTOMER_*` | — | Single sign-on per audience |
| `FD_METRICS_TOKEN` | — | Enables `/metrics`, which then requires this bearer token |

> **No SSO yet?** Staff accounts you create later in Admin → Staff can only sign in with a password if `FD_AUTH_STAFF_LOCAL=true`. Without that setting, only break-glass admins can.

The full list, with every module variable, is in [Configuration](docs/DOCUMENTATION.md#3-configuration) and [docs/configuration.md](docs/configuration.md).

## Single sign-on with Authentik

Staff and customers are separate login audiences, and each one gets its own OIDC client. In Authentik that means two OAuth2/OpenID providers and two applications. The blueprint at `deploy/authentik/featherdesk.blueprint.yaml` creates both, together with the default groups.

1. Import the blueprint in Authentik, or create the providers by hand.
2. Register these redirect URIs:
   - staff: `https://help.example.com/api/v1/auth/oidc/staff/callback`
   - customers: `https://help.example.com/api/v1/auth/oidc/customer/callback`
3. Optionally, register the back-channel logout URL: `https://help.example.com/api/v1/auth/oidc/staff/backchannel-logout`.
4. Map groups to roles, tiers and teams:

```env
FD_OIDC_STAFF_ISSUER=https://auth.example.com/application/o/featherdesk-staff/
FD_OIDC_STAFF_CLIENT_ID=…
FD_OIDC_STAFF_CLIENT_SECRET=…
FD_OIDC_STAFF_ROLE_MAP=fd-admins=admin,fd-supervisors=supervisor,fd-agents=agent
FD_OIDC_STAFF_TIER_MAP=fd-l2=2,fd-l3=3
FD_OIDC_STAFF_TEAM_MAP=fd-billing=Billing,fd-tech=Technical   # optional
```

How the mapping works:
- Roles and tiers are synced on **every** login, and the highest mapped value wins.
- Removing someone from a group demotes them at their next login. A user with no mapped group is refused and their account is disabled.
- Break-glass admins keep password access, so an IdP outage can't lock you out.

Step-by-step instructions, Keycloak notes and logout details are in [Single sign-on](docs/DOCUMENTATION.md#5-single-sign-on-sso).

## Roles and support levels

People are either **staff** or **customers**. The kind is fixed when the account is created, and customers can never reach staff screens or APIs.

Staff are placed on three independent axes:

| Axis | Values | Decides |
|---|---|---|
| **Role** | admin · supervisor · agent | What you may configure |
| **Tier** | 1 · 2 · 3 (labels editable, e.g. "Front desk / Specialist / Engineering") | How deep in the escalation chain you work. Admins and supervisors count as tier 3 |
| **Team** | any number (e.g. Billing, Technical) | Which queues you work |

- An agent sees a ticket if any of these holds:
  - it is in one of the agent's teams **and** at or below the agent's tier;
  - it is assigned to the agent;
  - the agent watches it.
- Unassigned tickets with no team sit in **triage**. Supervisors and admins always see triage. Agents see it while the setting "Agents can see the triage queue" is on, which it is by default.
- **Escalate** raises a ticket one tier and requires a reason. **De-escalate** lowers it one tier.
- Supervisors work all tickets and configure support: teams (including automatic assignment), categories, SLA, calendars, canned responses, automation rules, shared macros and knowledge-base categories.
- Admins can additionally manage staff, custom fields, branding, email and inbound mailboxes, support settings, the two-step verification policy, privacy tools, webhooks, all API tokens, the audit log and the system pages.
- Customers are **requesters**, who see their own tickets, or **organization managers**, who see their whole organization's tickets.

Reading something outside your visibility returns **404**. A forbidden action on something you can see returns **403**. Details are in [Concepts](docs/DOCUMENTATION.md#1-concepts).

## Projects

- **Keys and references:** every ticket belongs to a project and gets a reference from the project's own sequence, `KEY-123`, used in the UI, email subjects (`[WEB-12]`), search, the API (`/agent/tickets/by-ref/WEB-12`) and MCP (`ticket://WEB-12`). The global number (`#1042`) keeps working.
- **One project by default:** upgrading creates the **Default project** (`HD`) with everything in it, so a single-project installation behaves exactly as before.
- **Access:** agents see only the tickets of the projects they are members of; supervisors and admins see all. A **project admin** manages that project's categories, teams, SLA, KB categories, automation, macros, canned responses and portal branding.
- **Scoping:** categories and inbound mailboxes belong to a project; teams can serve several; SLA policies fall back to the global ones; rules, macros, canned responses, KB categories, custom fields and webhooks can be limited to projects.
- **Portal:** customers choose among the projects open to them (an organization can be limited to some); each portal-visible project has its own entry at `/p/{key}` with its own title, introduction, logo and colour.

- **Screens:** Administration → Projects (members and roles, team mapping, per-project SLA, portal branding and logo), project selectors on the configuration pages, a membership editor in Staff, a project picker in the portal, the branded entry `/p/{key}` and a by-project chart in the reports.

Guide: [docs/features/projects.md](docs/features/projects.md) · manual: [Projects](docs/DOCUMENTATION.md#721-projects-administration--projects-supervisor).

## Automation and assignment

- **Automatic assignment** per team: *Manual* (the default), *Round robin* or *Least busy*. It only picks agents who may own the ticket and are not marked **Away**, and runs whenever a ticket of the team ends up without an owner.
- **Automation rules** (supervisors and admins): *when a ticket is created, the customer replies, a ticket is updated, or every hour*, check conditions (text or pattern, channel, priority, status, tier, team, organization, email domain, tags, custom fields, business hours, hours since a change) and run up to 10 actions (set fields, tags, assign, note, canned reply, escalate, add watcher). A **dry run** tests a rule against a ticket.
- Rules run in the background as the **Automation** system user. Their changes never trigger other rules, and customers never see rule names.
- **Macros** bundle a reply, status, priority, tags and assignee for agents, personal or shared.

Guide: [docs/features/automation.md](docs/features/automation.md) · manual: [Automation and assignment](docs/DOCUMENTATION.md#716-automation-and-assignment-support-setup-supervisor).

## Two-step verification

- For **local accounts**, staff (including break-glass admins) and customers: an **authenticator app** (TOTP), a **code by email** (needs SMTP) and 10 **recovery codes**.
- Admins can require it for nobody, all staff or everyone (**Support settings → Security**). Users without a second factor set it up right after their password.
- **SSO accounts** use their identity provider's second factor. Personal access tokens are not affected.
- Lost device: an admin resets it, or `featherdesk reset-mfa --email …` on the server.

Guide: [docs/features/mfa.md](docs/features/mfa.md) · manual: [Two-step verification](docs/DOCUMENTATION.md#717-two-step-verification-system--support-settings-admin).

## Privacy and GDPR

- **Data export:** customers and staff download a ZIP of their personal data from **My data**; admins export anyone, optionally with internal data.
- **Erasure:** customers request deletion, admins approve or reject (optionally automatically after N days) or erase directly. Two modes: *anonymize* and *anonymize and redact*. Tickets stay as business records.
- **Retention policies:** a daily job anonymizes or deletes old closed tickets and removes raw inbound email, old audit entries and expired exports, with a preview first.
- **Consent:** with a privacy policy URL in Branding, registration records the acceptance.
- These are tools, not legal advice.

Guide: [docs/features/gdpr.md](docs/features/gdpr.md) · manual: [Privacy and GDPR](docs/DOCUMENTATION.md#718-privacy-and-gdpr-people--privacy-admin).

## Notification centre

- A **bell** with an unread badge in the agent panel and the portal, a dropdown with the newest notifications and a full page with filters. It updates live over the WebSocket and stays in sync across tabs.
- Each notification names the ticket by its reference (`WEB-123`) and shows its project chip (colour dot and key) when you work in more than one project.
- **Browser notifications** through the browser's Notification API, while a FeatherDesk tab is open in the background. There is no push service to run.
- Every user chooses per notification type: in the app, by email, as a browser pop-up.
- Works without SMTP. Notifications are kept 90 days, at most 500 per user.

Guide: [docs/features/notifications.md](docs/features/notifications.md) · manual: [Notification centre](docs/DOCUMENTATION.md#719-notification-centre).

## AI assistants (MCP)

A built-in [Model Context Protocol](https://modelcontextprotocol.io) server at `POST /mcp` lets AI assistants such as Claude Code, Claude Desktop and Cursor search, read and work tickets and read the knowledge base.

- **Off by default:** an admin turns it on under *Channels → Integrations → AI assistants (MCP)* (setting `integrations.mcp_enabled`, also `PATCH /api/v1/admin/integrations`); while off, `/mcp` answers 404.
- **The assistant is the user:** it connects with that user's personal access token and can never see or do more than the user could through the REST API with the same token. Customers' assistants get customer tools only and never see internal notes.
- **Tools:** `search_tickets`, `list_my_tickets`, `get_ticket`, `create_ticket`, `reply_to_ticket`, `update_ticket`, `search_kb`, `get_kb_article`, `list_categories`, and for staff `add_internal_note`, `escalate_ticket`, `deescalate_ticket`, `apply_macro`, `list_teams`. Resources `ticket://{ref}` (e.g. `ticket://WEB-12`) and `kb://{slug}`.
- **Protocol:** Streamable HTTP, revisions 2026-07-28 (stateless) and 2025-11-25/06-18/03-26 (sessions). No extra dependency or process.

```sh
claude mcp add --transport http featherdesk https://help.example.com/mcp \
  --header "Authorization: Bearer fd_pat_…"
```

The *API tokens* page shows the endpoint and ready-to-copy configs for Claude Code, Claude Desktop and Cursor. Guide: [docs/features/mcp.md](docs/features/mcp.md) · manual: [AI assistants (MCP)](docs/DOCUMENTATION.md#1010-ai-assistants-mcp).

## Slack

*Administration → Channels → Integrations* lists every integration as a card. **Slack** posts ticket activity to your channels, one thread per ticket:

- **Set up in three steps:** create your own Slack app from a pre-filled manifest (one click), paste its Client ID and Secret, and connect the workspace in a **popup** (OAuth). `FD_PUBLIC_URL` must be HTTPS.
- **Rules:** a channel (picker with public and private channels), the events (new tickets, status, assignment, priority, escalation, customer and agent replies, internal notes on request, SLA warnings and breaches, satisfaction ratings), project and priority filters, and a live preview of the posts.
- **Reliable:** retries with back-off on the built-in queue, channel problems shown on the rule, a revoked token shown with *Reconnect*. No extra process, no inbound endpoint.

Guide: [docs/features/slack.md](docs/features/slack.md) · manual: [Integrations](docs/DOCUMENTATION.md#722-integrations-channels--integrations-admin).

## API and WebSocket

The web app uses the same API that is available to integrations. The specification is [api/openapi.yaml](api/openapi.yaml) (OpenAPI 3.1). A running instance serves it at `/api/v1/openapi.yaml`, and as a readable reference page at `/api/docs`.

- **Authentication:**
  - The browser uses the session cookie and sends an `X-CSRF-Token` header on writes.
  - Scripts use a personal access token: `Authorization: Bearer fd_pat_…`.
  - Token scopes are `tickets:read`, `tickets:write`, `directory:read`, `directory:write`, `kb:read`, `kb:write` and `admin`. A token can never do more than its owner.
- **Errors** follow RFC 9457 (`application/problem+json`).
- **Lists** use cursor pagination (`?limit=…&cursor=…`).
- **Safer writes:** `Idempotency-Key` protects creates and replies against retries, and `If-Match` prevents lost updates.

```sh
curl -sS -X POST https://help.example.com/api/v1/agent/tickets \
  -H "Authorization: Bearer $API_TOKEN" -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"subject":"VPN is down","body_md":"Since 9:00.","requester_email":"ann@example.com","requester_name":"Ann"}'
```

- **WebSocket:** `GET /api/v1/ws` with the subprotocol `featherdesk.v1`.
  - Subscribe to topics such as `tickets`, `ticket:<id>`, `counts` and `me`.
  - After a reconnect, resume from the last event id you received.
  - Customer connections never receive internal notes.
- **Webhooks:** POST requests signed with HMAC-SHA256 in `X-FeatherDesk-Signature`.

Examples in curl, JavaScript, Python and Go are in [API](docs/DOCUMENTATION.md#10-rest-api), [WebSocket](docs/DOCUMENTATION.md#11-websocket-protocol) and [Webhooks](docs/DOCUMENTATION.md#12-webhooks).

## Operations at a glance

Every operational task is a subcommand of the same binary, because the image has no shell:

| Command | Purpose |
|---|---|
| `featherdesk serve` | Run the server (the default command) |
| `featherdesk migrate [status]` | Apply pending database migrations, or show their status |
| `featherdesk backup [--out DIR]` | Online backup: SQLite snapshot, attachments, and a manifest with checksums |
| `featherdesk restore --from DIR [--force]` | Restore a backup. Run it while the server is stopped |
| `featherdesk healthcheck` | Exits 0 when `/readyz` answers 200 (used as the container health check) |
| `featherdesk create-admin --email …` | Create a break-glass admin |
| `featherdesk reset-password --email …` | Set a new password and sign the user out everywhere |
| `featherdesk reset-mfa --email …` | Remove a user's two-step verification (lost device) |
| `featherdesk rotate-secrets` | Re-encrypt stored secrets with a new `FD_SECRET_KEY` (old key in `FD_SECRET_KEY_PREVIOUS`) |
| `featherdesk migrate-engine --from … --to …` | Copy all data from SQLite to PostgreSQL, or back |
| `featherdesk blob-migrate --to …` | Copy attachments between local disk and S3 |
| `featherdesk reindex` | Rebuild the ticket search index |
| `featherdesk config check` / `config docs` | Validate the configuration / print the variable reference |
| `featherdesk seed`, `seed --demo`, `seed-kb` | Demo data for evaluation |
| `featherdesk version` | Print the version and commit |

In Compose, run them as `docker compose -f deploy/compose.yml exec app /featherdesk <command>`.

- **Health:** `/healthz` (liveness) and `/readyz` (database, schema, storage and queue).
- **Monitoring:** `/metrics` (Prometheus format, needs `FD_METRICS_TOKEN`).
- **Backups, upgrades and troubleshooting:** see [Operations](docs/DOCUMENTATION.md#13-operations).

## Development

**Requirements**
- Go 1.27.3 or later (`CGO_ENABLED=0`; no C toolchain needed). Earlier 1.27 releases still trust the compromised eMudhra CAs.
- Node.js 24 with corepack (`corepack enable`; the repo uses pnpm 11).
- Docker, optional, for PostgreSQL and the Authentik/Mailpit dev stack.

```sh
make dev        # Go API on :8080 (FD_DEV=true, restarts on change) + Vite on :5173
make seed       # basic demo data (TICKETS=50); demo accounts have no password, use reset-password
make demo       # demo data for every feature (see Demo data below)
make dev-deps   # optional: Authentik (SSO) + Mailpit (email) in Docker
```

Open http://localhost:5173/agent. On the first start the dev server prints a setup token (`setup token: …`) for `/setup`. `make dev` turns on staff password login (`FD_AUTH_STAFF_LOCAL=true`).

- `make help` lists every target: `build`, `image`, `test`, `lint`, `e2e`, `footprint`, `loadtest`, `k8s-validate`, and more.
- The whole stack in containers, with the app built from source, Authentik with the FeatherDesk blueprint, and Mailpit: `docker compose -f deploy/compose.yml -f deploy/compose.dev.yml up -d --build`. The dev credentials are in `deploy/.env.dev`.
- Step by step from a clean clone (6 commands): [docs/dev/getting-started.md](docs/dev/getting-started.md).
- Repository layout, conventions and contribution rules are in [CONTRIBUTING.md](CONTRIBUTING.md) and [Development](docs/DOCUMENTATION.md#15-development-and-contributing).

## Demo data

To explore every feature with realistic content, fill a fresh installation with demo data:

```sh
docker compose -f deploy/compose.yml exec app /featherdesk seed --demo   # i.e. `featherdesk seed --demo`
make demo                                                                   # from a checkout, on the dev database
```

- It fills every feature, including projects, automation and assignment, two-step verification, privacy and the notification centre.
- It creates three projects: **Web App** `WEB` and **Mobile** `MOB` (both on the portal, `/p/WEB` and `/p/MOB`) and the staff-only **Internal IT** `IT`, with tickets in all three, a project admin and an agent who works in `MOB` only. The printout lists each project with sample references.
- It creates 4 organizations (one of them internal, limited to `IT`), 13 staff accounts of every role and tier, 14 customers, teams with calendars, categories, custom fields, SLA policies, about 60 tickets over the last two months, canned responses, saved views, a knowledge base in five languages, branding, a disabled webhook, an inactive mailbox and an admin API token. Nothing is sent anywhere.
- Logins include `admin@demo.test`, `supervisor@demo.test`, `freya.fields@demo.test` (project admin of `MOB`), `mona.mobile@demo.test` (works in `MOB` only), `tomas.tran@demo.test` (two-step verification on) and customers such as `mia.manager@acme.test`; the full table is in the [manual](docs/DOCUMENTATION.md#demo-data).
- It prints the entity counts, the portal and agent URLs and a login for every role. All accounts share the password `demo-password-123` (change it with `--password`). Staff other than the admin sign in locally only with `FD_AUTH_STAFF_LOCAL=true`; restart the app afterwards.
- `--tickets N` (default 60), `--smtp-mailpit` (deliver email to Mailpit on `localhost:1025`; otherwise email stays disabled) and `--force` (wipe and re-seed a database that an earlier `seed --demo` filled; a database with other data is always refused).

Details: [Development, Demo data](docs/DOCUMENTATION.md#demo-data).

## Testing

```sh
make test          # go test -race on SQLite AND PostgreSQL 18 (throwaway container) + web unit tests
make test-sqlite   # go test, SQLite only
make lint          # gofmt, go vet, golangci-lint, eslint, vue-tsc, SPDX headers, dependency gate
make e2e           # Playwright (chromium) against a fresh binary
```

- Every test that touches the store runs on **both** database engines. Without `FD_TEST_POSTGRES_URL`, the PostgreSQL half is skipped locally; in CI it fails instead.
- **E2E with real email.** With Mailpit running (`docker run -d -p 127.0.0.1:8025:8025 -p 127.0.0.1:1025:1025 -e MP_SMTP_DISABLE_RDNS=true axllent/mailpit`), `cd web && E2E_MAILPIT_URL=http://127.0.0.1:8025 pnpm exec playwright test` runs register → verify, password reset and invitations against a fresh server on chromium and mobile. `E2E_SERVER_BIN=bin/featherdesk` uses the binary from `make build`; `E2E_DATABASE_URL=postgres://…` (an empty database) runs the suite on PostgreSQL.
- **SSO interop.** With the dev stack up, `cd web && E2E_BASE_URL=http://localhost:8080 E2E_AUTHENTIK_URL=http://authentik.localhost:9000 pnpm exec playwright test e2e/sso.spec.ts` checks staff and customer SSO, group → role/tier sync and RP-initiated logout against Authentik.
- **Stack smoke (CI job `smoke`, DEP-022).** On a fresh dev stack started with `FD_DEV_BOOTSTRAP_ADMIN_EMAIL= FD_DEV_BOOTSTRAP_ADMIN_PASSWORD=` (no break-glass admin, so the app logs a setup token), `e2e/smoke-stack.spec.ts` runs the first-run wizard, staff SSO, customer register → ticket → live agent reply → escalate → resolve, and checks the reply mail in Mailpit. The exact commands are in the spec's header.
- **Operations drills.** `scripts/restore-drill.sh --engine sqlite|postgres` (backup → restore on new containers, compared table by table) and `scripts/upgrade-drill.sh --engine sqlite|postgres --from <previous image> --next-migration` (upgrade, downgrade refusal, rollback).
- **Integration containers.** `docker compose -f deploy/compose.test.yml up -d --wait` starts MinIO and Dovecot for `TestS3Integration` and `TestDovecotIntegration`; the commands are in that file.
- HTTP handlers are tested through the real router, and their responses are validated against `api/openapi.yaml`.
- Every route must be in the spec.

## Documentation map

| Topic | Where |
|---|---|
| Concepts: staff vs customers, roles × tiers × teams, lifecycle, SLA | [DOCUMENTATION §1](docs/DOCUMENTATION.md#1-concepts) |
| Installation: Compose, Postgres, S3, Kubernetes/Helm, proxies, TLS | [§2](docs/DOCUMENTATION.md#2-installation) |
| Configuration and env-vs-UI locking | [§3](docs/DOCUMENTATION.md#3-configuration), [configuration.md](docs/configuration.md) |
| First-run setup | [§4](docs/DOCUMENTATION.md#4-first-run-setup) |
| SSO (Authentik, Keycloak, any OIDC) | [§5](docs/DOCUMENTATION.md#5-single-sign-on-sso) |
| Email in and out | [§6](docs/DOCUMENTATION.md#6-email) |
| Admin, agent and customer guides | [§7](docs/DOCUMENTATION.md#7-admin-guide), [§8](docs/DOCUMENTATION.md#8-agent-guide), [§9](docs/DOCUMENTATION.md#9-customer-portal-guide) |
| REST API, WebSocket, webhooks | [§10](docs/DOCUMENTATION.md#10-rest-api), [§11](docs/DOCUMENTATION.md#11-websocket-protocol), [§12](docs/DOCUMENTATION.md#12-webhooks) |
| Operations, security model, FAQ | [§13](docs/DOCUMENTATION.md#13-operations), [§14](docs/DOCUMENTATION.md#14-security-model), [§16](docs/DOCUMENTATION.md#16-faq) |
| Automation and assignment, macros | [docs/features/automation.md](docs/features/automation.md) |
| Two-step verification (MFA) | [docs/features/mfa.md](docs/features/mfa.md) |
| Privacy and GDPR tools | [docs/features/gdpr.md](docs/features/gdpr.md) |
| Notification centre and browser notifications | [docs/features/notifications.md](docs/features/notifications.md) |
| AI assistants over MCP (Claude Code, Claude Desktop, Cursor) | [docs/features/mcp.md](docs/features/mcp.md) |
| Slack: app setup, popup sign-in, channel rules, threads | [docs/features/slack.md](docs/features/slack.md) |
| Backend internals for contributors | [docs/dev/internals.md](docs/dev/internals.md) |
| Changing a contract (API, events, schema, permissions) | [docs/dev/contract-changes.md](docs/dev/contract-changes.md) |
| Security reports | [SECURITY.md](SECURITY.md) |

## Licence

FeatherDesk is licensed under the **GNU Affero General Public License v3.0 only** (`AGPL-3.0-only`); see [LICENSE](LICENSE). Copyright © 2026 RadioBros.

FeatherDesk is developed by [RadioBros](https://radiobros.com/).

The third-party components shipped in the binary and the image, with their licences, are listed in [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES). Release history: [CHANGELOG.md](CHANGELOG.md).

- You may self-host, modify and redistribute it.
- If you run a modified version as a network service, you must offer its source code to that service's users.
