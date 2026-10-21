# Changelog

All notable changes to SimpleTickets are documented in this file.

The format is based on [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

**Rule for contributors:** every pull request that adds or changes a file under
`internal/store/migrations/` must add a `### Migrations` entry to the
`[Unreleased]` section below, saying what the migration does and whether it is
reversible, slow on large databases, or needs operator action. CI enforces this
(`scripts/changelog-check.sh`).

## [Unreleased]

### Added
- **Projects (D-20):** tickets belong to projects with their own key and references
  (`WEB-12`, `/agent/tickets/by-ref/{ref}`, `[WEB-12]` email subjects, `ticket://WEB-12`
  in MCP), categories, team mapping, per-project SLA overrides, mailboxes, KB categories,
  scoped rules, macros, canned responses, custom fields and webhooks, project members and
  project admins, organization limits and per-project portal branding (`/p/{key}`).
  Agents see only the tickets of their projects (invariant I-11). See
  `docs/features/projects.md`.

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

### Migrations
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

## [1.0.0] - 2026-10-21

First stable release of SimpleTickets, developed by [RadioBros](https://radiobros.com/). Everything below is new.

### Added
- **One container.** A static Go binary with the web app embedded: database, REST API,
  WebSocket hub, in-process job queue, scheduler and SPA. Distroless, non-root image,
  about 11 MB compressed, for `linux/amd64` and `linux/arm64`.
- **Customer portal** (`/`): tickets, replies, attachments, solve/reopen, satisfaction
  rating, organization managers, self-registration with email verification, magic links.
- **Agent panel** (`/agent`): live queues over WebSocket, filters and saved views,
  Markdown replies and internal notes, canned responses, @mentions, escalation between
  three support tiers, assignment, team transfer, watchers, links, merge and split,
  presence, business-hours SLA with warnings and breach alerts, reports.
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
  with scopes, WebSocket protocol `simpletickets.v1`, signed webhooks for 21 event types.
- **Storage:** SQLite by default, PostgreSQL 18 optional (`deploy/compose.postgres.yml`,
  `migrate-engine`), local or S3-compatible attachment storage (`blob-migrate`).
- **Operations:** online `backup`/`restore`, automatic migrations with downgrade refusal,
  Prometheus metrics, `/healthz` and `/readyz`, reverse-proxy examples (Caddy, nginx,
  Traefik), plain Kubernetes manifests and a Helm chart (single replica), optional
  Litestream replication overlay for SQLite (`deploy/compose.litestream.yml`).
- **Licence:** AGPL-3.0-only, SPDX headers in every source file, `THIRD_PARTY_NOTICES`.
- **Privacy (GDPR) tools:** self-service and admin data exports (a ZIP, downloadable for 7 days, with a ready email), customer deletion requests with admin approval or automatic approval, direct admin erasure (`anonymize` or `anonymize_and_redact`), retention policies with a dry-run preview, and consent recording at registration when a privacy policy URL is set. See `docs/features/gdpr.md`.
- `simpletickets rotate-secrets` re-encrypts every stored secret with `ST_SECRET_KEY`.
- **AI assistants (MCP):** a built-in Model Context Protocol server at `POST /mcp` (Streamable HTTP; protocol 2026-07-28 and 2025-11-25/06-18/03-26) for Claude Code, Claude Desktop, Cursor and other MCP clients, with ticket, knowledge-base and directory tools and `ticket://`/`kb://` resources. It authenticates with personal access tokens and runs every tool through the REST API's permission checks. Off by default (*Support settings → Integrations*, `PATCH /api/v1/admin/integrations`); the API tokens page shows ready-to-copy client configs. New `GET /api/v1/portal/tickets/by-number/{n}`. See `docs/features/mcp.md`.
- `serve` checks the database before migrating (SQLite `quick_check`) and reports it, the WAL size and the last backup under Admin → System.

### Migrations
- Initial schema, identical on SQLite and PostgreSQL (checked by `TestSchemaParity`):
  `0001_identity`, `0002_users_email_ci`, `0020_tickets`, `0021_links_fields`,
  `0040_support`, `0060_platform`, `0080_kb`, `0090_inbound`. Applied automatically on
  first start; no operator action needed. There is nothing to upgrade from.
- `0002_users_email_ci` (identity range, both engines): unique index on `lower(email)` for `users`. An upgrade fails if two users differ only by the case of their email; merge or rename one first. `serve` takes an automatic `pre-migrate-*.db` copy on SQLite before applying it.
- `0120_privacy` (privacy range, both engines): new tables `privacy_requests` and `user_consents`, and a nullable `tickets.anonymized_at` column. Additive and fast; no operator action.

### Changed
- `restore` and `migrate-engine` refuse while a server holds the database's single-instance lock; `restore --force` keeps the replaced database as `.pre-restore` and restores files into a `file://` blob root too.
- Postgres sessions run with `jit=off` unless the database URL sets `jit`.

[Unreleased]: https://github.com/radioBros/SimpleTickets/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/radioBros/SimpleTickets/releases/tag/v1.0.0
