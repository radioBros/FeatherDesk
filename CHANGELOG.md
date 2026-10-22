# Changelog

All notable changes to FeatherDesk are documented in this file.

The format is based on [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

**Rule for contributors:** every pull request that adds or changes a file under
`internal/store/migrations/` must add a `### Migrations` entry to the
`[Unreleased]` section below, saying what the migration does and whether it is
reversible, slow on large databases, or needs operator action. CI enforces this
(`scripts/changelog-check.sh`).

## [Unreleased]

## [1.0.0] - 2026-10-22

First stable release of FeatherDesk, developed by [RadioBros](https://radiobros.com/). Everything under *Added* is new; *Changed* and *Fixed* list what changed during the release candidates.

### Added
- **Branding by default:** the FeatherDesk logo is the default logo, favicon and email logo
  until a brand logo is uploaded. Shared links preview with the branded product name,
  description, logo and favicon (written into the page by the server); project links
  (`/p/KEY`) preview with the project's portal title and logo.
- **One container.** A static Go binary with the web app embedded: database, REST API,
  WebSocket hub, in-process job queue, scheduler and SPA. Distroless, non-root image,
  about 11 MB compressed, for `linux/amd64` and `linux/arm64`.
- **Customer portal** (`/`): tickets, replies, attachments, solve/reopen, satisfaction
  rating, organization managers, self-registration with email verification, magic links.
- **Customer portal switch:** *Support settings → Customer portal enabled*
  (`support.portal_enabled`, on by default, no environment variable). Off: only admins and
  staff sign in, every portal URL redirects to the staff sign-in, customer sessions, tokens
  and sockets are cut off; email in and out keeps working.
- **Agent panel** (`/agent`): live queues over WebSocket, filters and saved views,
  Markdown replies and internal notes, canned responses, @mentions, escalation between
  three support tiers, assignment, team transfer, watchers, links, merge and split,
  presence, business-hours SLA with warnings and breach alerts, reports.
- **Projects (D-20):** tickets belong to projects with their own key and references
  (`WEB-12`, `/agent/tickets/by-ref/{ref}`, `[WEB-12]` email subjects, `ticket://WEB-12`
  in MCP), a project switcher in the agent top bar, categories, team mapping, per-project
  SLA overrides, mailboxes, KB categories, scoped rules, macros, canned responses, custom
  fields and webhooks, project members and project admins, organization limits and
  per-project portal branding with its own entry at `/p/{key}`. Agents see only the
  tickets of their projects (invariant I-11). See `docs/features/projects.md`.
- **Automation and assignment (D-19):** per-team automatic assignment (manual, round robin,
  least busy) that skips agents marked **Away**; automation rules on ticket created,
  customer replied, ticket updated or hourly, with conditions, up to 10 actions and a dry
  run; personal and shared **macros**. See `docs/features/automation.md`.
- **Two-step verification (D-19):** authenticator app (TOTP), codes by email and 10
  recovery codes for local accounts; an admin policy (nobody, all staff, everyone); admin
  reset and `featherdesk reset-mfa`. See `docs/features/mfa.md`.
- **Privacy (GDPR) tools (D-19):** self-service and admin data exports (a ZIP, downloadable
  for 7 days, with a ready email), customer deletion requests with admin approval or
  automatic approval, direct admin erasure (`anonymize` or `anonymize_and_redact`),
  retention policies with a dry-run preview, and consent recording at registration when a
  privacy policy URL is set. See `docs/features/gdpr.md`.
- **Notification centre (D-19):** a bell with an unread badge, a dropdown and a full page in
  the agent panel and the portal, live over the WebSocket and in sync across tabs, browser
  notifications while a tab is open, per-type preferences for in-app, email and browser.
  Each entry names the ticket by its reference (`WEB-123`) and shows the project chip.
  See `docs/features/notifications.md`.
- **MCP server:** a built-in Model Context Protocol server at `POST /mcp` (Streamable HTTP;
  protocol 2026-07-28 and 2025-11-25/06-18/03-26) for MCP clients, with ticket,
  knowledge-base and directory tools and `ticket://`/`kb://` resources. It authenticates
  with personal access tokens and runs every tool through the REST API's permission
  checks. Off by default (`integrations.mcp_enabled`; *Support settings → Integrations*,
  `PATCH /api/v1/admin/integrations`); the API tokens page shows ready-to-copy client
  configs. New `GET /api/v1/portal/tickets/by-number/{n}`. See `docs/features/mcp.md`.
- **`FD_ALLOWED_ORIGINS`:** extra comma-separated browser origins (`scheme://host[:port]`)
  of the same instance, accepted by the CSRF, WebSocket and MCP Origin checks besides
  `FD_PUBLIC_URL`. With it, the container runs on any host port and address, e.g.
  `docker run -p 3200:8080 -e FD_PUBLIC_URL=http://192.168.1.50:3200
  -e FD_ALLOWED_ORIGINS=http://localhost:3200`. An invalid entry stops the server at start.
- **Interface:** a **Columns** button on every table (including the ticket list) to show or
  hide columns, remembered per user and per table; one compact field density across the
  app; an **eye toggle** on every password field to show or hide the password; a colourful
  theme with one contrast-checked hue per section (icons, page headers, chips, avatars)
  and distinct status and priority colours, in light and dark.
- **Administration:** first-run setup wizard protected by a one-time token, white-label
  branding, SMTP configured in the UI (with environment locks), teams, categories,
  custom fields, SLA policies and calendars, canned responses, tags, knowledge base in
  five languages, audit log, queue and failed-jobs view, system status.
- **Single sign-on** with Authentik or any OpenID Connect provider (tested with Keycloak),
  separately for staff and customers: group → role, tier and team on every login,
  RP-initiated and back-channel logout, break-glass local admins.
- **Email in and out:** templated notifications in English, Italian, German, French and
  Spanish; inbound IMAP (IDLE, password or XOAUTH2) and a raw-MIME webhook; threading,
  quote stripping with `original.eml` kept, loop protection, quarantine, staff replies
  and `#internal` notes by email.
- **Integrations:** REST API described by `api/openapi.yaml`, personal access tokens
  with scopes, WebSocket protocol `featherdesk.v1`, signed webhooks for 21 event types.
- **Storage:** SQLite by default, PostgreSQL 18 optional (`deploy/compose.postgres.yml`,
  `migrate-engine`), local or S3-compatible attachment storage (`blob-migrate`).
- **Operations:** online `backup`/`restore`, automatic migrations with downgrade refusal,
  Prometheus metrics, `/healthz` and `/readyz`, reverse-proxy examples (Caddy, nginx,
  Traefik), plain Kubernetes manifests and a Helm chart (single replica), optional
  Litestream replication overlay for SQLite (`deploy/compose.litestream.yml`).
- `serve` checks the database before migrating (SQLite `quick_check`) and reports it, the
  WAL size and the last backup under Admin → System.
- `featherdesk rotate-secrets` re-encrypts every stored secret with `FD_SECRET_KEY`.
- **Demo data:** `featherdesk seed --demo` (`make demo`) fills every feature, including
  the projects `WEB`, `MOB` and `IT`, automation, two-step verification, privacy and
  notifications, with one password for every demo account.
- **Licence:** AGPL-3.0-only, SPDX headers in every source file, `THIRD_PARTY_NOTICES`.

- **Integrations can post as the customer** (I-12). New PAT scope
  `tickets:act_as_customer` (admins only, never implied by `admin`) and
  `POST /api/v1/agent/tickets/{id}/customer-messages`
  `{on_behalf_of, body_md, attachment_ids?, set_status?: resolved}`: a system that owns
  the customer-facing UI posts the customer's reply through the portal reply path, so
  status changes, the reopen window, follow-ups, the first-response SLA, satisfaction
  surveys, notifications and the `message.created` event (actor = the customer) are those
  of a portal reply. Answers a minimal `{message, ticket_id, ref, status,
  followup_ticket?}`; supports `Idempotency-Key`; 60 writes per minute per token (429);
  audited as `ticket.message.on_behalf`; new problem slugs `on-behalf-not-customer`,
  `on-behalf-not-visible`, `on-behalf-disabled`. `POST /agent/tickets` accepts the
  scope alone: the first message is then authored by the requester (`channel: api`). Until
  now the first message of a ticket created there, and every message posted through
  `POST /agent/tickets/{id}/messages`, was authored by the token's staff owner; that is
  unchanged without the scope. Staff `Message` objects and webhook messages carry
  `via_integration` (staff also `via_integration_name`); agents see "via *token name*" in
  the thread, customers never do. The token form warns when the scope is checked.

### Changed
- `restore` and `migrate-engine` refuse while a server holds the database's single-instance
  lock; `restore --force` keeps the replaced database as `.pre-restore` and restores files
  into a `file://` blob root too.
- Postgres sessions run with `jit=off` unless the database URL sets `jit`.

### Fixed
- **Category-scoped custom fields no longer block tickets outside their categories.**
  A required field scoped to a category (the demo's `order_number` for Billing) was
  shown and required on the portal form whatever the category, and the server then
  rejected the ticket with 422 `cf.<key>` `not_applicable`. A field that does not apply
  to the ticket's category or project is now never required, and an empty value for it
  (or the value it already holds, e.g. after a category change) is ignored; only a real
  value is still 422 `not_applicable`. The portal custom-field lookup now returns
  `category_ids`, the portal and agent forms show, require and send a field only
  while it applies (and drop its value when the category or project changes), and
  required customer fields no longer block inbound e-mail tickets or follow-ups of
  closed tickets. An empty `category_ids`/`project_ids` list now means "all", like null.
- **Authentik blueprint, back-channel logout:** the providers' back-channel logout URL
  can now point somewhere other than `FD_PUBLIC_URL` through the optional Authentik
  environment variable `FD_BACKCHANNEL_BASE_URL`, for an Authentik worker that cannot
  reach the public URL (unset = `FD_PUBLIC_URL`, as before). The development stack sets
  it to `http://app:8080`: with `FD_PUBLIC_URL=http://localhost:8080` the worker was
  posting logout tokens to itself, so signing out at Authentik never ended the
  FeatherDesk session there.

### Migrations
All migrations exist for SQLite and PostgreSQL with identical results (checked by
`TestSchemaParity`) and are applied automatically when `serve` starts. On a new
installation there is nothing to do. The upgrade notes below concern databases created by
pre-release builds.
- Initial schema: `0001_identity`, `0020_tickets`, `0021_links_fields`, `0040_support`,
  `0060_platform`, `0080_kb`, `0090_inbound`. No operator action needed.
- `0002_users_email_ci` (identity range): unique index on `lower(email)` for `users`. An
  upgrade fails if two users differ only by the case of their email; merge or rename one
  first. `serve` takes an automatic `pre-migrate-*.db` copy on SQLite before applying it.
- `0100_automation`: `teams.assignment_strategy` and `teams.rr_last_user_id`,
  `users.away_since`, and the tables `automation_rules`, `automation_runs`,
  `automation_time_runs` and `macros`. Additive and fast; no operator action.
- `0110_mfa`: tables `user_mfa`, `mfa_recovery_codes` and `mfa_challenges`, and the
  `security` settings row (`mfa_required: none`). Additive and fast; no operator action.
- `0120_privacy`: tables `privacy_requests` and `user_consents`, and a nullable
  `tickets.anonymized_at` column. Additive and fast; no operator action.
- `0130_notifications`: table `notifications` (pruned to 90 days and 500 per user).
  Additive and fast; no operator action.
- `0131_ticket_search_narrow`: PostgreSQL only (the SQLite file only holds comments).
  Rebuilds `ticket_search` with `public_tsv` and one combined `staff_tsv`, each with a GIN
  index, so search reads far fewer heap pages. Copies the search table once and builds
  two GIN indexes, so it takes longer on large databases; no operator action.
- `0140_projects`: adds `projects`, `project_members`, `project_teams`,
  `organization_projects` and `users.current_project_id`, creates the **Default project**
  (key `HD`, named after the branding product name), makes every staff user a member and
  maps every team to it. Fast; no operator action.
- `0141_project_tickets`: adds `tickets.project_id` (every existing ticket joins the
  Default project) and `tickets.project_seq` (set to the ticket number, so `#1042`
  becomes `HD-1042`), its unique index and sequence counter. Rewrites every ticket row
  once (`UPDATE tickets SET project_seq = number`): allow a few seconds per 100 000
  tickets. Not reversible by a down migration (restore the pre-migration backup).
- `0142_project_scoping`: adds the project columns of categories, inbound mailboxes,
  KB categories, automation rules, macros, canned responses, custom fields and webhooks
  (existing categories and mailboxes join the Default project; everything else stays
  global), and rebuilds `sla_policies` with a surrogate `id` and `project_id` (existing
  rows become the global defaults). Fast; no operator action.
- `0150_integrations`: adds the nullable `ticket_messages.via_token_id` (foreign key to
  `api_tokens`, `ON DELETE SET NULL`) and a partial index on it. Additive and fast; no
  operator action.

[Unreleased]: https://github.com/radioBros/FeatherDesk/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/radioBros/FeatherDesk/releases/tag/v1.0.0
