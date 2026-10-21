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
